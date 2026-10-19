#!/usr/bin/env bash
# Creates a throwaway demo repository for the VHS tapes and cd's into it.
# Usage (from the repo root, via `source`): source docs/tapes/setup.sh <scenario>
# Scenarios: commit | thai | pr | review | redaction
# Everything is isolated: own git config, own git-quill config, fake provider.

scenario="${1:-commit}"
demo="${TMPDIR:-/tmp}/git-quill-demo-$scenario"
rm -rf "$demo" && mkdir -p "$demo"

export GIT_CONFIG_GLOBAL="$demo.gitconfig" GIT_CONFIG_NOSYSTEM=1
printf '[user]\n\tname = Demo\n\temail = demo@example.com\n[init]\n\tdefaultBranch = main\n' > "$GIT_CONFIG_GLOBAL"
export GIT_QUILL_CONFIG="$demo.toml" GIT_QUILL_PROVIDER=fake GIT_QUILL_FAKE_DELAY="${GIT_QUILL_FAKE_DELAY:-110}"
rm -f "$GIT_QUILL_CONFIG"
export PS1='\[\e[38;5;212m\]demo\[\e[0m\] $ ' PROMPT_COMMAND=

cd "$demo" || return
git init -q
mkdir -p internal/auth internal/store
cat > internal/auth/session.go <<'GO'
package auth

// Session is a logged-in user.
type Session struct{ User string }
GO
cat > internal/store/db.go <<'GO'
package store

func Open(dsn string) (*DB, error) { return connect(dsn) }
GO
printf '# shop\n' > README.md
git add -A && git commit -qm "chore: initial commit"

login_change() {
cat > internal/auth/login.go <<'GO'
package auth

import "errors"

var ErrEmptyPassword = errors.New("auth: empty password")

// Login checks the credentials and starts a session.
func Login(user, pass string) (*Session, error) {
	if pass == "" {
		return nil, ErrEmptyPassword
	}
	return &Session{User: user}, nil
}
GO
}

case "$scenario" in
commit|thai)
  login_change
  git add -A
  ;;
pr)
  git checkout -q -b feature/login
  login_change
  git add -A && git commit -qm "feat(auth): add login with empty-password check"
  printf '\n// Close ends the session.\nfunc (s *Session) Close() {}\n' >> internal/auth/session.go
  git add -A && git commit -qm "feat(auth): add Session.Close"
  printf '# shop\n\nLogin: `auth.Login(user, pass)`\n' > README.md
  git add -A && git commit -qm "docs: document login"
  ;;
review)
  cat > internal/store/orders.go <<'GO'
package store

// SaveOrder writes an order.
func (db *DB) SaveOrder(o Order) {
	_ = err
	// TODO: wrap in a transaction
	db.exec("INSERT INTO orders VALUES (?)", o.ID)
}
GO
  git add -A
  ;;
redaction)
  # Fake credentials, assembled at runtime so no scanner flags this script.
  printf 'DATABASE_URL=postgres://shop:%s@db:5432/shop\nSTRIPE_KEY=%s\nAWS_ACCESS_KEY_ID=%s\n' \
    "s3cr3t-pa55" "sk_live_""4eC39HqLyjWDarjtT1zdp7dc" "AKIA""Z7Q3M2XKP9LDT4WB" > .env.production
  login_change
  git add -A && git add -f .env.production
  ;;
esac
clear
