package signer

import (
	"io"
	"strings"
	"testing"
)

func TestSigningCommandPassesPassphraseOnlyThroughStdin(t *testing.T) {
	const secret = "fixture-passphrase"
	value := &GPGSigner{passphrase: []byte(secret)}
	command := value.signingCommand("--batch", "--clearsign", "input")
	for _, argument := range command.Args {
		if strings.Contains(argument, secret) {
			t.Fatal("signing command exposed passphrase in process arguments")
		}
	}
	if command.Stdin == nil {
		t.Fatal("signing command did not provide an explicit passphrase stream")
	}
	body, err := io.ReadAll(command.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != secret+"\n" {
		t.Fatal("signing command passphrase stream did not contain exact bytes")
	}
	if !containsArguments(command.Args, "--pinentry-mode", "loopback", "--passphrase-fd", "0") {
		t.Fatalf("signing command arguments = %v, want loopback passphrase fd", command.Args)
	}
}

func TestSigningCommandWithoutPassphraseDoesNotEnablePassphraseFD(t *testing.T) {
	command := (&GPGSigner{}).signingCommand("--batch", "--clearsign", "input")
	if command.Stdin != nil {
		t.Fatal("unencrypted signing command unexpectedly configured stdin")
	}
	if containsArguments(command.Args, "--passphrase-fd") {
		t.Fatal("unencrypted signing command unexpectedly configured passphrase fd")
	}
}

func containsArguments(arguments []string, expected ...string) bool {
	for start := 0; start+len(expected) <= len(arguments); start++ {
		match := true
		for offset := range expected {
			if arguments[start+offset] != expected[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
