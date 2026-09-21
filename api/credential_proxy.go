package api

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
)

func startCredentialProxyService(configPath string, port int, socket string) (*server.Service, error) {
	return startCredentialProxyServiceWithRepo(configPath, "", port, socket)
}

// startCredentialProxy runs the proxy service next to the API server, serving
// the definitions authorized by the user config and by the repository the
// server was started in.
func startCredentialProxy() (*server.Service, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repoPath := ""
	for _, name := range []string{"side.yml", "side.yaml", "side.toml", "side.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			repoPath = path
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return startCredentialProxyServiceWithRepo(common.GetSidekickConfigPath(), repoPath, server.HostPort, "")
}

func startCredentialProxyServiceWithRepo(configPath, repoPath string, port int, socket string) (*server.Service, error) {
	loadTrusted := func() (map[string]common.CredentialProxyConfig, error) {
		definitions := make(map[string]common.CredentialProxyConfig)
		for _, path := range []string{configPath, repoPath} {
			if path == "" {
				continue
			}
			config, err := common.LoadSidekickConfig(path)
			if err != nil {
				return nil, err
			}
			for name, definition := range config.CredentialProxies {
				if previous, exists := definitions[name]; exists && !reflect.DeepEqual(previous, definition) {
					return nil, errors.New("conflicting credential proxy definitions")
				}
				definitions[name] = definition
			}
		}
		if err := common.ValidateCredentialProxies(definitions); err != nil {
			return nil, err
		}
		return definitions, nil
	}
	definitions, err := loadTrusted()
	if err != nil {
		return nil, err
	}
	start := false
	allowSideSources := credential_proxy.SideSourcesAllowed()
	for _, definition := range definitions {
		if definition.Auth.Side == nil || allowSideSources {
			start = true
			break
		}
	}
	if !start {
		return nil, nil
	}
	if socket == "" {
		socket, err = server.ControlSocketPath()
		if err != nil {
			return nil, err
		}
	}
	return server.StartService(port, socket, loadTrusted, credential_proxy.SideSourcesAllowed)
}
