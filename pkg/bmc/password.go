package bmc

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// MaxIPMIPassword is the longest password IPMI 2.0 carries.
const MaxIPMIPassword = 20

// generatedLen fits IPMI and is ~95 bits of entropy.
const generatedLen = 16

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// PasswordPath is where a node's BMC password is kept (root only), for
// later runs and `bmbox credentials`.
func PasswordPath(dir string) string { return filepath.Join(dir, "password") }

// ResolvePassword returns the BMC password: the one given in the topology,
// else the one an earlier run stored, else a new random one. The result is
// stored, so the password only changes when the topology changes it.
func ResolvePassword(dir, given string, w FileWriter) (password string, generated bool, err error) {
	path := PasswordPath(dir)
	stored := ""
	if b, err := os.ReadFile(path); err == nil {
		stored = strings.TrimRight(string(b), "\n")
	}
	switch {
	case given != "":
		password = given
	case stored != "":
		password = stored
	default:
		if password, err = randomPassword(generatedLen); err != nil {
			return "", false, err
		}
		generated = true
	}
	if password != stored {
		if err := w.WriteSecret(path, []byte(password+"\n")); err != nil {
			return "", false, fmt.Errorf("store BMC password: %w", err)
		}
	}
	return password, generated, nil
}

// ReadPassword returns the stored password of a BMC.
func ReadPassword(dir string) (string, error) {
	b, err := os.ReadFile(PasswordPath(dir))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

func randomPassword(n int) (string, error) {
	max := big.NewInt(int64(len(passwordAlphabet)))
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[k.Int64()]
	}
	return string(b), nil
}
