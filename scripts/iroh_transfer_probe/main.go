package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"sidekick/iroh"

	"github.com/rs/zerolog/log"
	"github.com/tmc/go-iroh/endpointticket"
	irohlib "github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

func main() {
	bind := flag.String("bind", "127.0.0.1:0", "Local IP:port for the isolated endpoint")
	advertise := flag.String("advertise", "", "Concrete IP to advertise when binding a wildcard")
	withRelay := flag.Bool("relay", false, "Enable the default relay, as in the production endpoint")
	ticket := flag.String("ticket", "", "Dial this ticket instead of starting a server")
	size := flag.Int("bytes", 2*1024*1024, "Deterministic response payload size")
	rounds := flag.Int("rounds", 3, "Client transfers on one connection")
	duration := flag.Duration("duration", 5*time.Minute, "Maximum diagnostic lifetime")
	flag.Parse()
	if *size < 1 || *size > 64*1024*1024 || *rounds < 1 || *rounds > 20 || *duration <= 0 {
		log.Fatal().Msg("Invalid diagnostic limits")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var err error
	if *ticket != "" {
		err = runClient(ctx, *ticket, *size, *rounds)
	} else {
		err = runServer(ctx, *bind, *advertise, *withRelay, *size)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("iroh transfer diagnostic failed")
	}
}

func payload(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

func runServer(ctx context.Context, bind, advertise string, withRelay bool, size int) error {
	addr, err := netip.ParseAddrPort(bind)
	if err != nil {
		return err
	}
	ip := addr.Addr()
	if advertise != "" {
		ip, err = netip.ParseAddr(advertise)
		if err != nil {
			return err
		}
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("advertise must be a concrete address reachable by the client")
	}
	opts := []irohlib.Option{irohlib.WithBindAddr(addr), irohlib.WithALPNs(iroh.ALPN)}
	if withRelay {
		opts = append(opts, irohlib.WithRelayMode(relay.ModeDefault()))
	}
	ep, err := irohlib.Bind(ctx, opts...)
	if err != nil {
		return err
	}
	defer ep.Shutdown(context.Background())
	if withRelay {
		ready, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := ep.Online(ready); err != nil {
			return err
		}
	}
	address := netaddr.NewEndpointAddr(ep.ID()).WithIP(netip.AddrPortFrom(ip, ep.LocalAddr().Port()))
	if withRelay {
		for _, url := range ep.Addr().RelayURLs() {
			address = address.WithRelayURL(url)
		}
	}
	data := payload(size)
	digest := sha256.Sum256(data)
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Ticket string `json:"ticket"`
		NodeID string `json:"nodeId"`
		Bytes  int    `json:"bytes"`
		SHA256 string `json:"sha256"`
	}{
		endpointticket.Encode(address), ep.ID().String(), size, hex.EncodeToString(digest[:]),
	}); err != nil {
		return err
	}
	var sequence atomic.Int64
	for {
		conn, err := ep.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		id := sequence.Add(1)
		log.Info().Int64("connection", id).Msg("accepted connection")
		go func() {
			defer conn.Close()
			for {
				stream, err := conn.AcceptStreamConn(ctx)
				if err != nil {
					log.Info().Int64("connection", id).Err(err).Msg("connection ended")
					return
				}
				go serveStream(stream, id, data)
			}
		}()
	}
}

func serveStream(stream net.Conn, id int64, data []byte) {
	defer stream.Close()
	started := time.Now()
	if err := stream.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		log.Error().Err(err).Msg("set stream deadline")
		return
	}
	request, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		log.Warn().Int64("connection", id).Err(err).Msg("request read failed")
		return
	}
	request.Body.Close()
	log.Info().Int64("connection", id).Msg("request received")
	_, err = fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(data))
	if err != nil {
		log.Warn().Int64("connection", id).Err(err).Msg("response header failed")
		return
	}
	n, err := io.Copy(stream, bytes.NewReader(data))
	log.Info().Int64("connection", id).Int64("bytes", n).
		Dur("elapsed", time.Since(started)).Err(err).Msg("response write finished")
}

func runClient(ctx context.Context, ticket string, size, rounds int) error {
	address, err := endpointticket.Decode(ticket)
	if err != nil {
		return fmt.Errorf("invalid ticket")
	}
	ep, err := irohlib.Bind(ctx)
	if err != nil {
		return err
	}
	defer ep.Shutdown(context.Background())
	conn, err := ep.Connect(ctx, address, iroh.ALPN)
	if err != nil {
		return err
	}
	defer conn.Close()
	for round := 1; round <= rounds; round++ {
		if err := transfer(ctx, conn, size, round); err != nil {
			return err
		}
	}
	return nil
}

func transfer(ctx context.Context, conn *irohlib.Conn, size, round int) error {
	stream, err := conn.OpenStreamConn(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	if err := stream.SetDeadline(time.Now().Add(25 * time.Second)); err != nil {
		return err
	}
	started := time.Now()
	if _, err := io.WriteString(stream, "GET / HTTP/1.1\r\nHost: probe\r\nConnection: close\r\n\r\n"); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(stream), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(size)+1))
	if err != nil {
		return fmt.Errorf("round %d after %d bytes: %w", round, len(data), err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(data, payload(size)) {
		return fmt.Errorf("round %d: response payload mismatch", round)
	}
	log.Info().Int("round", round).Int("bytes", len(data)).
		Dur("elapsed", time.Since(started)).Msg("verified transfer")
	return nil
}
