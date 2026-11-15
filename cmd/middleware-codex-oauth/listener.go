package main

import (
	"errors"
	"net"
	"os"
	"strconv"

	"github.com/irinery/middlewareAuth/internal/config"
)

// inheritedListener accepts only an already-listening TCP loopback socket that
// matches the configured address. An invalid explicit setting never falls back
// to binding another socket.
func inheritedListener(value string, cfg config.HTTPConfig) (net.Listener, error) {
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil, errors.New("descritor invalido")
	}
	file := os.NewFile(uintptr(fd), "inherited-listener")
	if file == nil {
		return nil, errors.New("descritor indisponivel")
	}
	listener, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		return nil, errors.New("socket herdado indisponivel")
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	configuredIP := net.ParseIP(cfg.BindAddr)
	validHost := cfg.BindAddr == "localhost" || (configuredIP != nil && configuredIP.Equal(addrIP(addr)))
	if !ok || !addr.IP.IsLoopback() || addr.Port != cfg.Port || !validHost {
		_ = listener.Close()
		return nil, errors.New("socket herdado nao corresponde ao endereco configurado")
	}
	return listener, nil
}

func addrIP(addr *net.TCPAddr) net.IP {
	if addr == nil {
		return nil
	}
	return addr.IP
}
