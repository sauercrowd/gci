package server

import "fmt"

type Server struct {
	Name       string
	User       string
	Host       string
	PrivateKey string
	ServiceDir string
}

func ResolveServer(name string) (Server, error) {
	resolvedName := name
	if resolvedName == "" {
		defaultServerName, found, err := DefaultServerName()
		if err != nil {
			return Server{}, err
		}
		if !found {
			return Server{}, fmt.Errorf("no server configured; pass --server or set server in service config")
		}
		resolvedName = defaultServerName
	}

	entries, err := load()
	if err != nil {
		return Server{}, err
	}

	for _, entry := range entries {
		if entryName(entry) == resolvedName {
			return Server{
				Name:       entryName(entry),
				User:       entry.User,
				Host:       entry.Host,
				PrivateKey: entry.PrivateKey,
				ServiceDir: entryServiceDir(entry),
			}, nil
		}
	}

	return Server{}, fmt.Errorf("server %q not found", resolvedName)
}
