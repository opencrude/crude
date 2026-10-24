package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
)

// This model intentionally never reads RemoteAccess fields or uses its registry
// decoder, name validators, challenge structs, or lifetime constants. Random
// output is retained only as an opaque handle; admission and durable state are
// predicted from the submitted actions, not from the owner's response or View.
type remoteAuthFuzzDevice struct {
	credential coreprotocol.RemoteCredential
	view       coreprotocol.RemoteDevice
}

type remoteAuthFuzzChallenge struct {
	code       string
	expires    time.Time
	generation int
	failures   int
	spent      bool
}

type remoteAuthFuzzBootstrap struct {
	credential coreprotocol.RemoteCredential
	expires    time.Time
	generation int
	spent      bool
}

type remoteAuthFuzzAudit struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	DeviceID string    `json:"device_id,omitempty"`
}

func FuzzCoreRemoteAuthentication(f *testing.F) {
	// Bytes select one of 28 actions; the quotient selects an older handle.
	for _, seed := range [][]byte{
		{},
		{0, 1, 1, 9, 10, 11, 19, 12, 13, 9, 19}, // one use, revoke, reopen
		{0, 0, 2, 1, 13, 9},                     // replacement fences old code
		{0, 13, 1},                              // restart forgets challenge
		{0, 21, 1},                              // exact five-minute boundary
		{0, 3, 4, 5, 1, 9},                      // wrong/invalid/cancelled
		{0, 20, 1, 7, 1, 0, 1},                  // explicit rate limit/exhaustion
		{0, 1, 0, 14, 1, 9, 19, 15, 1, 9},       // disable invalidates challenge
		{14, 16, 18, 17, 17, 19},                // SSH while direct disabled
		{16, 22, 17, 16, 17, 17},                // SSH exact expiry/replay
		{16, 13, 17, 16, 17},                    // restart forgets bootstrap
		{0, 1, 0, 1, 12, 13, 9, 37, 40, 13, 9},  // older device remains distinct
		{24, 25, 0, 26, 1, 16, 27, 17, 23},      // cancellation cannot consume
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 48 {
			actions = actions[:48]
		}
		ctx := t.Context()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		dir := remotePrivateTestDir(t)
		now := time.Unix(1800000000, 0).UTC()
		cfg := remoteTestConfig()
		endpoints := SSHUpgradeEndpoints{QUICAddress: "127.0.0.1:17340", TLSAddress: "127.0.0.1:17341"}
		var owner *RemoteAccess
		var fingerprint string
		generation := 0
		open := func() {
			t.Helper()
			var err error
			owner, err = OpenRemoteAccess(dir, RemoteAccessOptions{Config: cfg, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if fingerprint != "" && owner.Identity().Fingerprint != fingerprint {
				t.Fatal("reopen changed host identity")
			}
			fingerprint = owner.Identity().Fingerprint
			if fingerprint == "" {
				t.Fatal("missing host identity")
			}
			// These are inert published facts: no listener or external network is
			// created. Reopening the app republishes its existing live endpoints.
			if err := owner.Configure(cfg, endpoints); err != nil {
				t.Fatal(err)
			}
			generation++
		}
		open()
		var devices []remoteAuthFuzzDevice
		var challenges []remoteAuthFuzzChallenge
		var bootstraps []remoteAuthFuzzBootstrap
		var audits []remoteAuthFuzzAudit
		var secrets []string
		var window time.Time
		var attempts []time.Time
		persisted := false
		step := -1
		assertError := func(label string, got, want error) {
			t.Helper()
			if !errors.Is(got, want) {
				t.Fatalf("step %d %s: error %v, want %v", step, label, got, want)
			}
		}
		selectDevice := func(selector int) *remoteAuthFuzzDevice {
			if len(devices) == 0 {
				return nil
			}
			return &devices[len(devices)-1-selector%len(devices)]
		}
		selectBootstrap := func(selector int) *remoteAuthFuzzBootstrap {
			if len(bootstraps) == 0 {
				return nil
			}
			return &bootstraps[len(bootstraps)-1-selector%len(bootstraps)]
		}
		create := func(callCtx context.Context) {
			t.Helper()
			var want error
			if callCtx.Err() != nil {
				want = context.Canceled
			} else if !cfg.Enabled {
				want = ErrRemoteDisabled
			}
			code, err := owner.CreatePairing(callCtx)
			assertError("create", err, want)
			if want != nil {
				if code != (coreprotocol.RemotePairingCode{}) {
					t.Fatal("rejected create returned private evidence")
				}
				return
			}
			if code.Code == "" || code.Fingerprint != fingerprint || !code.ExpiresAt.Equal(now.Add(5*time.Minute)) {
				t.Fatal("pairing lifetime/identity mismatch")
			}
			challenges = append(challenges, remoteAuthFuzzChallenge{code: code.Code, expires: now.Add(5 * time.Minute), generation: generation})
			secrets = append(secrets, code.Code)
		}
		pair := func(callCtx context.Context, code, name string, validName bool) {
			t.Helper()
			var want error
			var current *remoteAuthFuzzChallenge
			if len(challenges) != 0 {
				current = &challenges[len(challenges)-1]
			}
			if callCtx.Err() != nil {
				want = context.Canceled
			} else if !cfg.Enabled {
				want = ErrRemoteDisabled
			} else {
				if window.IsZero() || !now.Before(window.Add(time.Minute)) {
					window, attempts = now, nil
				}
				if len(attempts) == 5 {
					want = ErrPairingLimited
				} else {
					attempts = append(attempts, now)
					if current == nil || current.generation != generation || current.spent || code != current.code || !now.Before(current.expires) || current.failures >= 5 || !validName {
						want = ErrRemoteCredential
						if current != nil {
							current.failures++
						}
					}
				}
			}
			credential, err := owner.Pair(callCtx, coreprotocol.RemotePairRequest{Code: code, DeviceName: name})
			assertError("pair (including rate-limit outcome)", err, want)
			if want != nil && credential != (coreprotocol.RemoteCredential{}) {
				t.Fatal("rejected pairing returned credential")
			}
			switch want {
			case nil:
				if credential.DeviceID == "" || credential.Token == "" || credential.Fingerprint != fingerprint {
					t.Fatal("successful pairing lacks device credential")
				}
				for _, d := range devices {
					if d.credential.DeviceID == credential.DeviceID || d.credential.Token == credential.Token {
						t.Fatal("pairing reused device identity or secret")
					}
				}
				current.spent = true
				devices = append(devices, remoteAuthFuzzDevice{credential: credential, view: coreprotocol.RemoteDevice{ID: credential.DeviceID, Name: name, CreatedAt: now}})
				secrets = append(secrets, credential.Token)
				audits = append(audits, remoteAuthFuzzAudit{At: now, Kind: "device_paired", DeviceID: credential.DeviceID})
				persisted = true
			case ErrRemoteCredential:
				audits = append(audits, remoteAuthFuzzAudit{At: now, Kind: "pairing_rejected"})
				persisted = true
			}
		}
		authDevice := func(callCtx context.Context, d *remoteAuthFuzzDevice, wrong bool, bearer bool) {
			t.Helper()
			id, token := "unknown-device", "wrong-token"
			want := error(ErrRemoteCredential)
			if d != nil {
				id, token = d.credential.DeviceID, d.credential.Token
				if !wrong && cfg.Enabled && d.view.RevokedAt.IsZero() {
					want = nil
				}
			}
			if wrong {
				token += "x"
			}
			if callCtx.Err() != nil {
				want = context.Canceled
			}
			var p RemotePrincipal
			var err error
			if bearer {
				p, err = owner.AuthenticateCredential(callCtx, id+"."+token)
			} else {
				p, err = owner.Authenticate(callCtx, id, token)
			}
			assertError("device authentication", err, want)
			if want == nil {
				if p.DeviceID != id || p.SSHBootstrap {
					t.Fatal("paired device gained SSH authority")
				}
				d.view.LastSeen = now
			} else if p != (RemotePrincipal{}) {
				t.Fatal("rejected token returned authority")
			}
		}
		authBootstrap := func(callCtx context.Context, b *remoteAuthFuzzBootstrap, wrong bool) {
			t.Helper()
			id, token := "unknown-bootstrap", "wrong-token"
			want := error(ErrRemoteCredential)
			if b != nil {
				id, token = b.credential.DeviceID, b.credential.Token
				if !wrong && !b.spent && b.generation == generation && now.Before(b.expires) {
					want = nil
				}
			}
			if wrong {
				token += "x"
			}
			if callCtx.Err() != nil {
				want = context.Canceled
			}
			p, err := owner.Authenticate(callCtx, id, token)
			assertError("SSH authentication", err, want)
			if want == nil {
				b.spent = true
				if !p.SSHBootstrap || p.DeviceID != "" {
					t.Fatal("bootstrap became durable paired-device authority")
				}
			} else if p != (RemotePrincipal{}) {
				t.Fatal("rejected bootstrap returned authority")
			}
		}
		check := func() {
			t.Helper()
			view, err := owner.View(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if view.Config != cfg || view.Fingerprint != fingerprint || len(view.Devices) != len(devices) {
				t.Fatalf("step %d: public configuration/device inventory diverged", step)
			}
			for i, d := range devices {
				if view.Devices[i] != d.view {
					t.Fatalf("step %d device %d: public state diverged from submitted actions", step, i)
				}
				// Every transition rechecks existing identities, especially after
				// stale pairing/authentication inputs, disable, and reopen.
				want := error(nil)
				if !cfg.Enabled {
					want = ErrRemoteDisabled
				} else if !d.view.RevokedAt.IsZero() {
					want = ErrRemoteCredential
				}
				assertError("check retained device", owner.CheckDevice(ctx, d.view.ID), want)
			}
			public, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "devices.json"))
			if err != nil && (!errors.Is(err, os.ErrNotExist) || persisted) {
				t.Fatal(err)
			}
			for _, secret := range secrets {
				if bytes.Contains(raw, []byte(secret)) || bytes.Contains(public, []byte(secret)) {
					t.Fatalf("step %d: plaintext pairing/device/bootstrap secret leaked", step)
				}
			}
			if bytes.Contains(public, []byte("hash")) || bytes.Contains(public, []byte("token")) {
				t.Fatal("public device view exposed credential fields")
			}
			if len(raw) == 0 {
				return
			}
			// Independently decode disk, never decodeRemoteRegistry. This
			// catches ghost enrollments and revoked-device resurrection even
			// if the public View implementation hides either defect.
			var disk struct {
				Version int `json:"version"`
				Devices []struct {
					View coreprotocol.RemoteDevice `json:"view"`
					Hash string                    `json:"hash"`
				} `json:"devices"`
				Audit []remoteAuthFuzzAudit `json:"audit"`
			}
			if err := json.Unmarshal(raw, &disk); err != nil {
				t.Fatal(err)
			}
			if disk.Version != 1 || len(disk.Devices) != len(devices) {
				t.Fatalf("step %d: durable device inventory diverged", step)
			}
			for i, d := range devices {
				digest := sha256.Sum256([]byte(d.credential.Token))
				if disk.Devices[i].View != d.view || disk.Devices[i].Hash != hex.EncodeToString(digest[:]) {
					t.Fatalf("step %d: durable device %d differs from model", step, i)
				}
			}
			wantAudit := audits
			if len(wantAudit) > 256 {
				wantAudit = wantAudit[len(wantAudit)-256:]
			}
			if !reflect.DeepEqual(disk.Audit, wantAudit) {
				t.Fatalf("step %d: durable audit diverged (got %d events, want %d)", step, len(disk.Audit), len(wantAudit))
			}
		}
		check()
		for i, action := range actions {
			step = i
			op, selector := int(action)%28, int(action)/28
			latestCode := ""
			if len(challenges) != 0 {
				latestCode = challenges[len(challenges)-1].code
			}
			switch op {
			case 0:
				create(ctx)
			case 1, 2, 3, 4, 5:
				code, name, valid, callCtx := latestCode, "fuzz-device", true, ctx
				switch op {
				case 2:
					code = "no-stale-challenge"
					if len(challenges) > 1 {
						code = challenges[len(challenges)-2-selector%(len(challenges)-1)].code
					}
				case 3:
					code += "!"
				case 4:
					name, valid = []string{"", " ", "bad\nname", strings.Repeat("x", 257), "\xff"}[selector%5], false
				case 5:
					callCtx = cancelled
				}
				pair(callCtx, code, name, valid)
			case 6, 7, 8:
				now = now.Add([]time.Duration{time.Second, time.Minute, 5 * time.Minute}[op-6])
			case 9, 10, 11, 26:
				d, callCtx := selectDevice(selector), ctx
				switch op {
				case 11:
					d = nil
				case 26:
					callCtx = cancelled
				}
				authDevice(callCtx, d, op == 10, op == 9)
			case 12:
				d := selectDevice(selector)
				id, want := "unknown-device", error(ErrRemoteCredential)
				if d != nil {
					id, want = d.view.ID, nil
				}
				assertError("revoke", owner.Revoke(ctx, id), want)
				if d != nil && d.view.RevokedAt.IsZero() {
					d.view.RevokedAt = now
					audits = append(audits, remoteAuthFuzzAudit{At: now, Kind: "device_revoked", DeviceID: id})
				}
			case 13:
				open()
				window, attempts = time.Time{}, nil
			case 14, 15:
				cfg.Enabled = op == 15
				if err := owner.Configure(cfg, endpoints); err != nil {
					t.Fatal(err)
				}
				if !cfg.Enabled && len(challenges) != 0 {
					challenges[len(challenges)-1].spent = true
				}
			case 16, 25:
				callCtx, want := ctx, error(nil)
				if op == 25 {
					callCtx, want = cancelled, context.Canceled
				}
				b, err := owner.BootstrapSSH(callCtx, "verified-SSH-device")
				assertError("SSH bootstrap", err, want)
				if want == nil {
					if b.Credential.DeviceID == "" || b.Credential.Token == "" || b.Credential.Fingerprint != fingerprint || b.QUICAddress != endpoints.QUICAddress || b.TLSAddress != endpoints.TLSAddress || !b.ExpiresAt.Equal(now.Add(time.Minute)) {
						t.Fatal("SSH bootstrap identity/endpoint/lifetime mismatch")
					}
					bootstraps = append(bootstraps, remoteAuthFuzzBootstrap{credential: b.Credential, expires: now.Add(time.Minute), generation: generation})
					secrets = append(secrets, b.Credential.Token)
				} else if b != (coreprotocol.RemoteSSHBootstrapResult{}) {
					t.Fatal("cancelled bootstrap returned private evidence")
				}
			case 17, 18, 27:
				callCtx := ctx
				if op == 27 {
					callCtx = cancelled
				}
				authBootstrap(callCtx, selectBootstrap(selector), op == 18)
			case 19:
				id, want := "unknown-device", error(ErrRemoteCredential)
				if d := selectDevice(selector); d != nil {
					id = d.view.ID
					if d.view.RevokedAt.IsZero() {
						want = nil
					}
				}
				if !cfg.Enabled {
					want = ErrRemoteDisabled
				}
				assertError("explicit device check", owner.CheckDevice(ctx, id), want)
			case 20:
				for range 6 {
					pair(ctx, "wrong-code", "rate-limit-probe", true)
				}
			case 21:
				if len(challenges) != 0 && now.Before(challenges[len(challenges)-1].expires) {
					now = challenges[len(challenges)-1].expires
				} else {
					now = now.Add(5 * time.Minute)
				}
			case 22:
				if b := selectBootstrap(selector); b != nil && now.Before(b.expires) {
					now = b.expires
				} else {
					now = now.Add(time.Minute)
				}
			case 23:
				p, err := owner.AuthenticateCredential(ctx, []string{"", "a.b", ".", strings.Repeat("x", 129), "a.b.c"}[selector%5])
				assertError("malformed bearer", err, ErrRemoteCredential)
				if p != (RemotePrincipal{}) {
					t.Fatal("malformed bearer returned authority")
				}
			case 24:
				create(cancelled)
			}
			check()
		}
		// The final owner is genuinely reopened even for sequences with no
		// explicit restart. Recheck every persisted credential after reopen,
		// including revoked and temporarily disabled paired devices.
		step = len(actions)
		open()
		for i := range devices {
			authDevice(ctx, &devices[i], false, true)
		}
		for i := range bootstraps {
			authBootstrap(ctx, &bootstraps[i], false)
		}
		check()
		// String formatting is a separate public projection from JSON View.
		for _, d := range devices {
			if strings.Contains(fmt.Sprintf("%v %#v", d.credential, d.credential), d.credential.Token) {
				t.Fatal("credential formatting leaked token")
			}
		}
	})
}
