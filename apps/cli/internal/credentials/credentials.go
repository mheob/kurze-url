// Package credentials stores the CLI's tokens: in the operating system's
// keychain when there is one, in a private file when there is not.
//
// The refresh token is a long-lived credential, so the keychain is the home
// the spec prefers. A file is the fallback for machines without one (a
// headless Linux box has no Secret Service), and it is never the first
// choice: it holds exactly what the keychain would, so the only protection
// left is its 0600 mode.
//
// One environment is one entry, keyed by a name the caller derives from the
// Supabase URL. The package never sees that URL; it takes the name as an
// opaque string, so a production login and a preview login cannot overwrite
// each other.
//
// No error this package returns contains a token. Errors name what failed and
// where, and wrap the operating system's own error, which never quotes the
// payload; a document that cannot be decoded is reported without the decoder's
// message, because for a syntax error that message quotes a byte of the input.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zalando/go-keyring"
)

// service is the keychain service name. The environment name is the keychain
// user, so every environment gets an entry of its own under one service.
const service = "kurze-url"

// ErrNotFound means nothing is stored for the environment: nobody has logged
// in, or the person has logged out. It is the one error callers turn into
// "run login"; every other failure is something broken.
var ErrNotFound = errors.New("no stored credentials")

// ErrKeychainUnavailable is wrapped into the error Delete returns when the
// keychain failed with anything but "not found": it could not be reached, is
// locked, or refused for some other reason. A token may be sitting in it
// (see Delete), so the caller must not report a clean logout, but it can tell
// that case from a failure of its own: `logout` words it as a warning, so a
// headless machine whose keychain has never worked still ends cleanly.
var ErrKeychainUnavailable = errors.New("the keychain could not be used")

// errNoEnvironment is what every operation returns for an empty environment
// name. It is a mistake in the caller, not a missing login, so it must never
// be ErrNotFound: that would send a person to `login`, which would fail the
// same way. Without this check the file name would be "credentials-.json" and
// every environment with an empty name would share one keychain entry.
var errNoEnvironment = errors.New("credentials: the environment name is empty")

// errNoDirectory is the same kind of caller mistake for an empty directory.
// filepath.Join would turn it into a bare file name, which is the working
// directory, and a token file beside whatever the person happens to be
// working on is worse than a refused Save.
var errNoDirectory = errors.New("credentials: the directory for the credentials file is empty")

// Tokens is what a login produces and a refresh replaces. It is stored as one
// JSON document, in the keychain entry or in the file, so the two stores
// stay interchangeable.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Store keeps one environment's tokens. Load returns ErrNotFound when there
// are none, and Delete is not an error when there is nothing to delete. A
// Chain's Delete does report a keychain it could not use, as an error that
// matches ErrKeychainUnavailable, even when it removed everything else.
type Store interface {
	Load() (Tokens, error)
	Save(Tokens) error
	Delete() error
}

// FallbackReporter lets a command tell the person, once, that their tokens
// are in a file and not in a keychain, and where that file is. The spec's
// "no silent fallback on a shared machine" rule rests on it.
type FallbackReporter interface {
	// FellBack reports whether the latest Save went to the file.
	FellBack() bool
	// FilePath is where the file is, or would be.
	FilePath() string
}

// Chain is the Store the CLI runs on: the keychain first, the file when the
// keychain cannot be used. The zero value is not usable; get one from New.
//
// It is safe for concurrent use. The keychain does its own locking, the file
// is replaced by rename and never rewritten in place, and the one piece of
// state here, whether the latest Save fell back, is atomic.
type Chain struct {
	envName string
	dir     string

	// fellBack is the outcome of the latest Save that stored anything. A
	// failed Save leaves it alone, because nothing moved.
	fellBack atomic.Bool
}

var (
	_ Store            = (*Chain)(nil)
	_ FallbackReporter = (*Chain)(nil)
)

// New returns the Chain for one environment. envName becomes the keychain user
// and part of the file name, so it must already be safe for both: the
// environment package reduces it to lowercase letters, digits and hyphens. dir
// is where the fallback file goes, and it is created on first use.
//
// New cannot fail, so an empty envName or dir is refused by the operations
// instead.
func New(envName, dir string) *Chain {
	return &Chain{envName: envName, dir: dir}
}

// Load returns the stored tokens. The keychain is asked first and the file is
// read as well. When the keychain has no entry, is unavailable, or holds
// something that is not a token document, the file's tokens are returned, and
// a missing file is ErrNotFound. A damaged file is an error only when the
// keychain has nothing usable either: it must not break a login the keychain
// holds.
//
// When both stores hold tokens, the one that expires later wins, and the
// keychain on a tie. Both exist after this sequence, with no outside edit: a
// login stores its tokens in the keychain; the keychain becomes unusable (a
// locked macOS keychain over SSH, no Secret Service in a headless session) and
// Load reports "not logged in"; the next login falls back to the file. When
// the keychain recovers, its old entry must not shadow the newer login in the
// file, and a later login or rotation always expires later. Load changes
// nothing: the older copy stays until a keychain Save overwrites it and
// removes the file, or Delete reports the keychain.
//
// A keychain failure is deliberately not reported. A machine whose keychain is
// down is the machine the file exists for, and a login that fell back to the
// file keeps working after the keychain returns, until the next Save moves it
// there. The cost is that a keychain which is merely locked reads as "not
// logged in".
func (c *Chain) Load() (Tokens, error) {
	if err := c.check(); err != nil {
		return Tokens{}, err
	}
	var fromKeychain Tokens
	haveKeychain := false
	if raw, err := keyring.Get(service, c.envName); err == nil {
		if tokens, derr := decode([]byte(raw)); derr == nil {
			fromKeychain, haveKeychain = tokens, true
		}
	}
	fromFile, ferr := readFile(c.FilePath())

	switch {
	case haveKeychain && ferr == nil && fromFile.ExpiresAt.After(fromKeychain.ExpiresAt):
		return fromFile, nil
	case haveKeychain:
		return fromKeychain, nil
	default:
		return fromFile, ferr
	}
}

// Save stores the tokens in the keychain, and in the file only when the
// keychain refuses them (any error: no Secret Service, a locked keychain, an
// entry too large for the platform). FellBack tells the two apart afterwards.
//
// A successful keychain write removes a file an earlier fallback left behind,
// so a stale token never sits on disk beside a newer one. If that removal
// fails, Save reports it even though the keychain already holds the new
// tokens: silence would leave a secret in a place nobody knows about.
func (c *Chain) Save(tokens Tokens) error {
	if err := c.check(); err != nil {
		return err
	}
	data, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}

	kerr := keyring.Set(service, c.envName, string(data))
	if kerr == nil {
		c.fellBack.Store(false)
		if err := removeFile(c.FilePath()); err != nil {
			return fmt.Errorf("remove the stale credentials file: %w", err)
		}
		return nil
	}

	if ferr := writeFile(c.FilePath(), data); ferr != nil {
		return fmt.Errorf("store credentials: keychain: %w; file: %w", kerr, ferr)
	}
	c.fellBack.Store(true)
	return nil
}

// Delete removes the tokens from the keychain and the file. Both are always
// attempted, so a failing keychain cannot stop the file from going, and
// nothing being there is not an error.
//
// A keychain failure other than "not found" is always returned, wrapped so
// errors.Is(err, ErrKeychainUnavailable) holds, and joined with the file's
// error when both fail. Finding and removing a fallback file does not excuse
// it, because the file does not prove the keychain is empty: a login stores
// its tokens in the keychain; the keychain becomes unusable (a locked macOS
// keychain over SSH, no Secret Service in a headless session); the next login
// falls back to the file; and the old entry is still in the keychain. A
// logout that removed the file and answered "done" would leave that token
// behind. The caller words the error: `logout` prints it as a warning, so a
// machine whose keychain never worked ends cleanly, while a token that may
// survive is never hidden.
func (c *Chain) Delete() error {
	if err := c.check(); err != nil {
		return err
	}
	var errs []error
	if err := keyring.Delete(service, c.envName); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		errs = append(errs, fmt.Errorf("%w: delete from the keychain: %w", ErrKeychainUnavailable, err))
	}
	if err := removeFile(c.FilePath()); err != nil {
		errs = append(errs, fmt.Errorf("delete the credentials file: %w", err))
	}
	return errors.Join(errs...)
}

// FellBack reports whether the latest Save went to the file.
func (c *Chain) FellBack() bool {
	return c.fellBack.Load()
}

// FilePath returns where the fallback file is, or would be: the directory the
// Chain was given plus credentials-<environment>.json. It is empty when either
// part is missing, because neither makes a valid file.
func (c *Chain) FilePath() string {
	if c.envName == "" || c.dir == "" {
		return ""
	}
	return filepath.Join(c.dir, "credentials-"+c.envName+".json")
}

// check refuses the inputs New cannot: an empty environment name or an empty
// directory. Both are refused by every operation, also those that would only
// touch the keychain, so a Chain that is wrong never works until the day the
// keychain fails.
func (c *Chain) check() error {
	if c.envName == "" {
		return errNoEnvironment
	}
	if c.dir == "" {
		return errNoDirectory
	}
	return nil
}

// decode reads a stored token document. It reports only that the document is
// unusable, never why, so no byte of it can reach an error message.
func decode(data []byte) (Tokens, error) {
	var tokens Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return Tokens{}, errors.New("stored credentials are not a valid token document")
	}
	return tokens, nil
}

// Memory is a Store that keeps the tokens in memory, for other packages'
// tests: the login flow, the refreshing session and the commands all need a
// Store, and none of their tests may touch a keychain or a file. It is a
// regular file and not a _test.go one because those packages import it.
//
// Memory is safe for concurrent use. The zero value is an empty Memory, but
// NewMemory is the documented way to get one.
type Memory struct {
	// FallbackPath makes the Memory pose as a Chain that fell back to a file:
	// when it is not empty, FellBack is true and FilePath returns it. Set it
	// before the Memory is shared, because the field itself is not guarded.
	FallbackPath string

	mu     sync.Mutex
	tokens Tokens
	stored bool
}

var (
	_ Store            = (*Memory)(nil)
	_ FallbackReporter = (*Memory)(nil)
)

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{}
}

// Load returns the saved tokens, or ErrNotFound when nothing was saved or the
// tokens were deleted since.
func (m *Memory) Load() (Tokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stored {
		return Tokens{}, ErrNotFound
	}
	return m.tokens, nil
}

// Save replaces the stored tokens.
func (m *Memory) Save(tokens Tokens) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = tokens
	m.stored = true
	return nil
}

// Delete forgets the stored tokens. Having none is not an error.
func (m *Memory) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = Tokens{}
	m.stored = false
	return nil
}

// FellBack is true when FallbackPath is set.
func (m *Memory) FellBack() bool {
	return m.FallbackPath != ""
}

// FilePath returns FallbackPath.
func (m *Memory) FilePath() string {
	return m.FallbackPath
}
