// Package hypervisor talks to libvirtd over its native RPC protocol (pure Go,
// no cgo) to provision storage and define the virtual bare-metal nodes.
package hypervisor

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/digitalocean/go-libvirt"
)

const DefaultURI = "qemu:///system"

type Client struct {
	l   *libvirt.Libvirt
	URI string
}

func Connect(uri string) (*Client, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid libvirt URI %q: %w", uri, err)
	}
	l, err := libvirt.ConnectToURI(u)
	if err != nil {
		return nil, fmt.Errorf("connect to libvirt at %s: %w", uri, err)
	}
	return &Client{l: l, URI: uri}, nil
}

func (c *Client) Close() error { return c.l.Disconnect() }

// Version returns the libvirt daemon version as "major.minor.micro".
func (c *Client) Version() (string, error) {
	v, err := c.l.ConnectGetLibVersion()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", v/1000000, (v/1000)%1000, v%1000), nil
}

func isErr(err error, code libvirt.ErrorNumber) bool {
	var e libvirt.Error
	return errors.As(err, &e) && e.Code == uint32(code)
}
