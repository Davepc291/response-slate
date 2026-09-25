package authhttp

// fakeMFAProvider fakes the cryptographic WebAuthn ceremony for this
// package's own HTTP-transport tests (routing, auth, CSRF, cookie
// plumbing), mirroring identityservice's own test fake for the identical
// reason: these two packages test different layers and each needs its own
// lightweight double. A "finish" request whose "credential" field decodes
// to exactly validMFAResponseMarker is treated as a successful ceremony;
// anything else fails verification, exactly like a real Provider would for
// a malformed or forged response.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/mfa"
)

const validMFAResponseMarker = "synthetic-valid-webauthn-response"

type fakeMFAProvider struct {
	mu      sync.Mutex
	counter int
}

func (f *fakeMFAProvider) BeginEnrollment(user mfa.EnrollmentUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return &protocol.CredentialCreation{}, &webauthn.SessionData{Challenge: "synthetic-challenge", UserID: user.WebAuthnID()}, nil
}

func (f *fakeMFAProvider) FinishEnrollment(user mfa.EnrollmentUser, sessionData webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	var marker string
	if err := json.Unmarshal(responseJSON, &marker); err != nil || marker != validMFAResponseMarker {
		return nil, errors.New("fake: malformed or unverifiable response")
	}
	f.mu.Lock()
	f.counter++
	id := f.counter
	f.mu.Unlock()
	return &webauthn.Credential{
		ID:        []byte(fmt.Sprintf("synthetic-credential-%d", id)),
		PublicKey: []byte(fmt.Sprintf("synthetic-public-key-%d", id)),
	}, nil
}

func (f *fakeMFAProvider) BeginLogin(user mfa.LoginUser) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if len(user.Credentials) == 0 {
		return nil, nil, errors.New("fake: no credentials to verify possession of")
	}
	return &protocol.CredentialAssertion{}, &webauthn.SessionData{Challenge: "synthetic-login-challenge", UserID: user.WebAuthnID()}, nil
}

func (f *fakeMFAProvider) FinishLogin(user mfa.LoginUser, sessionData webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	var marker string
	if err := json.Unmarshal(responseJSON, &marker); err != nil || marker != validMFAResponseMarker {
		return nil, errors.New("fake: malformed or unverifiable response")
	}
	if len(user.Credentials) == 0 {
		return nil, errors.New("fake: no credentials to verify possession of")
	}
	cred := user.Credentials[0]
	return &cred, nil
}
