package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
)

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:8855", "Local Sidekick API URL")
	workspace := flag.String("workspace", "", "Read this workspace's tasks over iroh")
	mode := flag.String("address-mode", "fresh", "Address hints: fresh, relay-only, stale, identity-only")
	discovery := flag.Bool("discovery", false, "Enable N0 DNS discovery on the diagnostic client")
	flag.Parse()
	options := probeOptions{workspace: *workspace, mode: *mode, discovery: *discovery}
	if err := options.validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := diagnose(strings.TrimRight(*baseURL, "/"), options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func diagnose(baseURL string, options probeOptions) (result error) {
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Post(
		baseURL+"/api/v1/remote/pairings/",
		"application/json",
		bytes.NewBufferString(`{"name":"ticket-reachability-diagnostic"}`),
	)
	if err != nil {
		return fmt.Errorf("pairing request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("pairing request returned HTTP %d", response.StatusCode)
	}
	var pairing struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
		Ticket string `json:"ticket"`
		Token  string `json:"token"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&pairing)
	if pairing.Device.ID == "" {
		return fmt.Errorf("pairing response has no device ID; cannot revoke pairing")
	}
	defer func() {
		request, err := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/remote/pairings/"+pairing.Device.ID, nil)
		if err != nil {
			result = fmt.Errorf("could not construct pairing revocation request")
			return
		}
		response, err := client.Do(request)
		if err != nil {
			result = fmt.Errorf("pairing revocation failed")
			return
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			result = fmt.Errorf("pairing revocation returned HTTP %d", response.StatusCode)
			return
		}
		fmt.Println("temporary pairing revoked")
	}()
	if decodeErr != nil {
		return fmt.Errorf("invalid pairing response")
	}
	address, err := endpointticket.Decode(pairing.Ticket)
	if err != nil {
		return fmt.Errorf("ticket decoding failed")
	}
	fmt.Printf("endpointId=%s relayPresent=%t directAddressCount=%d\n", address.ID, len(address.RelayURLs()) > 0, len(address.IPAddrs()))
	for _, direct := range address.IPAddrs() {
		ip := direct.Addr()
		class := "global"
		switch {
		case ip.IsUnspecified():
			class = "wildcard"
		case ip.IsLoopback():
			class = "loopback"
		case ip.IsLinkLocalUnicast():
			class = "link-local"
		case ip.IsPrivate():
			class = "private"
		case !ip.IsGlobalUnicast():
			class = "non-unicast"
		}
		fmt.Printf("directAddressClass=%s ipv4=%t\n", class, ip.Is4())
	}
	if options.workspace != "" {
		return probeTasks(address, pairing.Token, options)
	}
	return nil
}
