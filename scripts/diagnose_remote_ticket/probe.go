package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	sideiroh "sidekick/iroh"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

type probeOptions struct {
	workspace string
	mode      string
	discovery bool
}

func (o probeOptions) validate() error {
	switch o.mode {
	case "fresh", "relay-only", "stale", "identity-only":
		return nil
	default:
		return fmt.Errorf("unknown address mode %q", o.mode)
	}
}

func probeAddress(address netaddr.EndpointAddr, mode string) netaddr.EndpointAddr {
	switch mode {
	case "relay-only":
		result := netaddr.NewEndpointAddr(address.ID)
		for _, relayURL := range address.RelayURLs() {
			result = result.WithRelayURL(relayURL)
		}
		return result
	case "stale":
		staleRelay, err := netaddr.ParseRelayURL("https://stale-relay.invalid/")
		if err != nil {
			panic(err)
		}
		return netaddr.NewEndpointAddr(address.ID).
			WithRelayURL(staleRelay).
			WithIP(netip.MustParseAddrPort("192.0.2.1:9"))
	case "identity-only":
		return netaddr.NewEndpointAddr(address.ID)
	default:
		return address
	}
}

func probeTasks(address netaddr.EndpointAddr, token string, options probeOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	address = probeAddress(address, options.mode)
	bindOptions := []iroh.Option{iroh.WithRelayMode(relay.ModeDefault())}
	if options.discovery {
		bindOptions = append(bindOptions, iroh.WithDNSResolver(nil))
	}
	endpoint, err := iroh.Bind(ctx, bindOptions...)
	if err != nil {
		return fmt.Errorf("endpoint bind: %w", err)
	}
	defer endpoint.Shutdown(context.Background())
	conn, err := endpoint.Connect(ctx, address, sideiroh.ALPN)
	if err != nil {
		return fmt.Errorf("connect after %s: %w", time.Since(started).Round(time.Millisecond), err)
	}
	defer conn.Close()
	stream, err := conn.OpenStreamConn(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://sidekick/api/v1/workspaces/"+url.PathEscape(options.workspace)+"/tasks/", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Close = true
	if err := request.Write(stream); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(stream), request)
	if err != nil {
		return fmt.Errorf("read response headers: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("task request returned HTTP %d", response.StatusCode)
	}
	var body struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<20))
	if err := decoder.Decode(&body); err != nil {
		return fmt.Errorf("decode tasks: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("unexpected data after task response: %v", err)
	}
	fmt.Printf("tasks=%d elapsed=%s mode=%s discovery=%t relayHints=%d directHints=%d\n",
		len(body.Tasks), time.Since(started).Round(time.Millisecond), options.mode, options.discovery,
		len(address.RelayURLs()), len(address.IPAddrs()))
	return nil
}