// SPDX-License-Identifier: AGPL-3.0-or-later

// Package upnp opens the WebRTC UDP port on the home router (IGD) so the phone
// can reach the PC directly even from behind a carrier-grade NAT.
package upnp

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

const lease = time.Hour

type portMapper interface {
	AddPortMapping(remoteHost string, extPort uint16, proto string, intPort uint16, intClient string, enabled bool, desc string, leaseSec uint32) error
	DeletePortMapping(remoteHost string, extPort uint16, proto string) error
	GetExternalIPAddress() (string, error)
}

type Mapping struct {
	ExternalIP string
	Port       uint16

	client   portMapper
	internal string
	stop     chan struct{}
	once     sync.Once
}

// Map forwards external UDP <port> to this host's <port> and keeps the lease
// alive until Close.
func Map(port uint16, desc string) (*Mapping, error) {
	internal, err := outboundIP()
	if err != nil {
		return nil, err
	}
	clients := discover()
	if len(clients) == 0 {
		return nil, errors.New("no UPnP IGD found (disabled on the router?)")
	}
	for _, c := range clients {
		if err := c.AddPortMapping("", port, "UDP", port, internal, true, desc, uint32(lease/time.Second)); err != nil {
			continue
		}
		ext, err := c.GetExternalIPAddress()
		ip := net.ParseIP(ext)
		if err != nil || ip == nil || !isPublic(ip) {
			// Double NAT: the router itself is behind another NAT, useless here.
			_ = c.DeletePortMapping("", port, "UDP")
			return nil, fmt.Errorf("router external IP %q is not public (double NAT)", ext)
		}
		m := &Mapping{ExternalIP: ext, Port: port, client: c, internal: internal, stop: make(chan struct{})}
		go m.renew(desc)
		return m, nil
	}
	return nil, errors.New("router refused the UDP port mapping")
}

func (m *Mapping) renew(desc string) {
	t := time.NewTicker(lease / 2)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := m.client.AddPortMapping("", m.Port, "UDP", m.Port, m.internal, true, desc, uint32(lease/time.Second)); err != nil {
				log.Printf("upnp: renew failed: %v", err)
			}
		case <-m.stop:
			return
		}
	}
}

func (m *Mapping) Close() {
	m.once.Do(func() {
		close(m.stop)
		_ = m.client.DeletePortMapping("", m.Port, "UDP")
	})
}

func discover() []portMapper {
	var out []portMapper
	if cs, _, err := internetgateway2.NewWANIPConnection2Clients(); err == nil {
		for _, c := range cs {
			out = append(out, c)
		}
	}
	if cs, _, err := internetgateway2.NewWANIPConnection1Clients(); err == nil {
		for _, c := range cs {
			out = append(out, c)
		}
	}
	if cs, _, err := internetgateway2.NewWANPPPConnection1Clients(); err == nil {
		for _, c := range cs {
			out = append(out, c)
		}
	}
	return out
}

// outboundIP returns the LAN address used for the default route (no packet is sent).
func outboundIP() (string, error) {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPublic(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}
