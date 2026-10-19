package redact

import (
	"strings"
	"testing"
)

// Fake secrets are assembled at runtime so this file never contains a
// literal that secret scanners (gitleaks, GitHub push protection) would flag.
func fake(parts ...string) string { return strings.Join(parts, "") }

var (
	awsID     = fake("AKIA", "Z7Q3", "M2XK", "P9LD", "T4WB")
	awsSecret = fake("wJalrXUtnF", "EMI/K7MDEN", "G/bPxRfiCY", "zq8Hk2Lm4N")
	ghp       = fake("ghp_", strings.Repeat("aB3dE5", 6))
	ghPat     = fake("github_pat_", "11ABCDEFG0", "abcdefghijkl_", strings.Repeat("Zx9", 19))
	glpat     = fake("glpat-", "xY9zA1bC2dE3fG4hI5jK")
	slackBot  = fake("xoxb-", "123456789012-", "1234567890123-", "AbCdEfGhIjKlMnOpQrStUvWx")
	slackHook = fake("https://hooks.slack.com/services/", "T00000000/", "B00000000/", "XXXXXXXXXXXXXXXXXXXXXXXX")
	antKey    = fake("sk-ant-", "api03-", strings.Repeat("Qw3rTy", 8))
	oaiKey    = fake("sk-proj-", strings.Repeat("Lk8Jh7", 8))
	gKey      = fake("AIza", "SyA1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q")
	stripe    = fake("sk_", "live_", "4eC39HqLyjWDarjtT1zdp7dc")
	npmTok    = fake("npm_", strings.Repeat("a1B2c3", 6))
	jwt       = fake("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", ".", "eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ", ".", "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c")
	pemBegin  = fake("-----BEGIN ", "RSA PRIVATE KEY-----")
	pemEnd    = fake("-----END ", "RSA PRIVATE KEY-----")
	envRandom = fake("Zk9", "qL2", "xW7", "vB4", "nM8", "pR1", "tY6", "cD3")
)

func TestRedactPatterns(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		in     string
		want   string // substring that must be present after redaction
		leak   string // substring that must NOT be present
		types  []string
		config bool
	}{
		{"aws access key id", "deploy.sh", "+export AWS_ACCESS_KEY_ID=" + awsID, "[REDACTED:aws-access-key-id]", awsID, []string{"aws-access-key-id"}, false},
		{"aws secret", "config.py", `+aws_secret_access_key = "` + awsSecret + `"`, "[REDACTED:aws-secret-access-key]", awsSecret, []string{"aws-secret-access-key"}, false},
		{"github classic", "ci.yml", "+  token: " + ghp, "[REDACTED:github-token]", ghp, []string{"github-token"}, false},
		{"github fine-grained", "README.md", "+use " + ghPat + " here", "[REDACTED:github-token]", ghPat, []string{"github-token"}, false},
		{"gitlab", "x.go", `+const t = "` + glpat + `"`, "[REDACTED:gitlab-token]", glpat, []string{"gitlab-token"}, false},
		{"slack bot", "bot.js", `+const slack = '` + slackBot + `'`, "[REDACTED:slack-token]", slackBot, []string{"slack-token"}, false},
		{"slack webhook", "notify.sh", "+curl -X POST " + slackHook, "[REDACTED:slack-webhook]", "XXXXXXXXXXXXXXXXXXXXXXXX", []string{"slack-webhook"}, false},
		{"anthropic", "a.py", `+client = Anthropic(api_key="` + antKey + `")`, "[REDACTED:anthropic-api-key]", antKey, []string{"anthropic-api-key"}, false},
		{"openai", "b.ts", "+const k = `" + oaiKey + "`", "[REDACTED:openai-api-key]", oaiKey, []string{"openai-api-key"}, false},
		{"google", "maps.js", "+key=" + gKey, "[REDACTED:google-api-key]", gKey, []string{"google-api-key"}, false},
		{"stripe", "pay.rb", "+Stripe.api_key = '" + stripe + "'", "[REDACTED:stripe-key]", stripe, []string{"stripe-key"}, false},
		{"npm", ".npmrc", "+//registry.npmjs.org/:_authToken=" + npmTok, "[REDACTED:npm-token]", npmTok, []string{"npm-token"}, false},
		{"jwt", "test.http", "+Cookie: session=" + jwt, "[REDACTED:jwt]", jwt, []string{"jwt"}, false},
		{"bearer", "api.sh", `+curl -H "Authorization: Bearer abcDEF1234567890ghiJKL" x`, "Bearer [REDACTED:bearer-token]", "abcDEF1234567890ghiJKL", []string{"bearer-token"}, false}, // gitleaks:allow (fake fixture)
		{"basic", "api.sh", `+Authorization: Basic dXNlcjpodW50ZXIyMjIy`, "Basic [REDACTED:basic-auth]", "dXNlcjpodW50ZXIyMjIy", []string{"basic-auth"}, false},
		{"url creds", "db.go", `+dsn := "postgres://admin:s3cr3tP4ss@db:5432/app"`, "admin:[REDACTED:url-credentials]@db", "s3cr3tP4ss", []string{"url-credentials"}, false},
		{"password quoted", "main.go", `+	password := "hunter2!"`, `password := "[REDACTED:password]"`, "hunter2!", []string{"password"}, false},
		{"json secret", "creds.json", `+  "client_secret": "q8F2kLm9",`, `"client_secret": "[REDACTED:secret-assignment]"`, "q8F2kLm9", []string{"secret-assignment"}, false},
		{"yaml unquoted", "app.yaml", "+  db_password: hunter22", "db_password: [REDACTED:password]", "hunter22", []string{"password"}, true},
		{"env file entropy", ".env.production", "+STRIPE_WEBHOOK=" + envRandom, "STRIPE_WEBHOOK=[REDACTED:high-entropy-env]", envRandom, []string{"high-entropy-env"}, true},
		{"env file quoted entropy", "config/.env", `+SIGNING="` + envRandom + `"`, `SIGNING="[REDACTED:high-entropy-env]"`, envRandom, []string{"high-entropy-env"}, true},
		{"removed line still redacted", "x.env", "-API_TOKEN=" + ghp, "[REDACTED:github-token]", ghp, []string{"github-token"}, true},
		{"code unquoted high entropy", "x.go", "+	apiKey = Ab12Cd34Ef56Gh78", "apiKey = [REDACTED:secret-assignment]", "Ab12Cd34Ef56Gh78", []string{"secret-assignment"}, false}, // gitleaks:allow (fake fixture)
		{"two on one line", "x.sh", "+A=" + ghp + " B=" + glpat, "A=[REDACTED:github-token] B=[REDACTED:gitlab-token]", glpat, []string{"github-token", "gitlab-token"}, false},
	}
	r := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fs := r.Diff(tt.file, tt.in)
			if !strings.Contains(got, tt.want) {
				t.Errorf("got %q\nwant substring %q", got, tt.want)
			}
			if strings.Contains(got, tt.leak) {
				t.Errorf("secret leaked: %q", got)
			}
			var types []string
			for _, f := range fs {
				types = append(types, f.Type)
				if f.File != tt.file || f.Line != 1 {
					t.Errorf("finding %+v has wrong file/line", f)
				}
			}
			if strings.Join(types, ",") != strings.Join(tt.types, ",") {
				t.Errorf("types = %v, want %v", types, tt.types)
			}
			if got[0] != tt.in[0] {
				t.Errorf("diff prefix not preserved: %q", got)
			}
		})
	}
}

func TestFalsePositives(t *testing.T) {
	cases := []struct{ file, line string }{
		{"main.go", `+	password := os.Getenv("DB_PASSWORD")`},
		{"main.go", `+	token, err := auth.NewToken(ctx)`},
		{"main.go", `+	if password == "" {`},
		{"main.go", `+	if token != expected {`},
		{"main.go", `+	cfg.Secret = opts.Secret`},
		{"main.go", `+	tokens := strings.Fields(line)`},
		{"main.go", `+	Token string ` + "`json:\"token\"`"},
		{"main.go", `+	errBadToken = errors.New("invalid token supplied")`},
		{"main.go", `+	const tokenURL = "https://oauth2.example.com/token"`},
		{"api.ts", `+  password: string;`},
		{"api.ts", `+  const apiKey = process.env.API_KEY;`},
		{"settings.py", `+SECRET_KEY = os.environ["SECRET_KEY"]`},
		{"app.yaml", `+  max_tokens: 4096`},
		{"app.yaml", `+  password: ${DB_PASSWORD}`},
		{"app.yaml", `+  api_key: <your-api-key>`},
		{"app.yaml", `+  secret: !vault |`},
		{"README.md", `+Set password=changeme in the example config.`},
		{"README.md", `+export OPENAI_API_KEY=sk-...`},
		{".env.example", `+API_KEY=your-api-key-here`},
		{".env", `+DATABASE_URL=postgres://localhost:5432/app`},
		{".env", `+LOG_LEVEL=debug`},
		{".env", `+GREETING="hello world from the app"`},
		{".env", `+BUILD_DIR=/var/lib/app/build/output/dir`},
		{".env", `+FEATURE_FLAG_NAME=enable_new_checkout_flow`},
		{"x.go", `+	sha := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`},
		{"db.go", `+	dsn := "postgres://user:${PASS}@db/app"`},
		{"x.go", `+	// the access_key is rotated daily`},
		{"x.go", `+	password = xxxxxxxx`},
		{"x.go", `+	authorization: "Bearer " + token,`},
	}
	r := New()
	for _, c := range cases {
		got, fs := r.Diff(c.file, c.line)
		if len(fs) != 0 || got != c.line {
			t.Errorf("false positive in %s:\n in: %q\nout: %q\nfindings: %+v", c.file, c.line, got, fs)
		}
	}
}

func TestPrivateKeyMultiline(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/deploy/id_rsa b/deploy/id_rsa",
		"+++ b/deploy/id_rsa",
		"@@ -0,0 +1,5 @@",
		"+" + pemBegin,
		"+MIIEpAIBAAKCAQEA7bq0",
		"+ZmFrZWtleWJvZHlsaW5l",
		"+" + pemEnd,
		"+after",
	}, "\n")
	got, fs := New().Diff("deploy/id_rsa", in)
	if strings.Contains(got, "MIIEpAIBAAKCAQEA7bq0") || strings.Contains(got, "ZmFrZWtleWJvZHlsaW5l") {
		t.Fatalf("key body leaked:\n%s", got)
	}
	if len(fs) != 1 || fs[0].Type != "private-key" || fs[0].Line != 4 {
		t.Errorf("findings = %+v", fs)
	}
	if strings.Count(got, "\n") != strings.Count(in, "\n") {
		t.Error("line count changed; review line numbers would shift")
	}
	if !strings.HasSuffix(got, "+after") || !strings.Contains(got, "+++ b/deploy/id_rsa") {
		t.Errorf("unexpected output:\n%s", got)
	}

	// Single-line (escaped) key inside JSON.
	one := `+  "private_key": "` + pemBegin + `\nMIIBOgIBAAJBAKj34\n` + pemEnd + `\n",`
	got, fs = New().Diff("sa.json", one)
	if strings.Contains(got, "MIIBOgIBAAJBAKj34") || len(fs) == 0 || fs[0].Type != "private-key" {
		t.Errorf("inline key: %q %+v", got, fs)
	}
}

func TestTextMode(t *testing.T) {
	msg := "fix: rotate leaked key " + ghp + "\n\n-the old one was " + awsID
	got, fs := New().Text(msg)
	if strings.Contains(got, ghp) || strings.Contains(got, awsID) || len(fs) != 2 {
		t.Errorf("got %q %+v", got, fs)
	}
	if !strings.HasPrefix(strings.Split(got, "\n")[2], "-the old one") {
		t.Error("text mode must not treat leading '-' as a diff prefix")
	}
	if fs[0].File != "" {
		t.Error("text findings should have no file")
	}
}

func TestSummary(t *testing.T) {
	fs := []Finding{
		{Type: "github-token", File: "b.sh"},
		{Type: "aws-access-key-id", File: "a.env"},
		{Type: "github-token", File: "b.sh"},
		{Type: "jwt"},
	}
	got := strings.Join(Summary(fs), "; ")
	want := "jwt (commit messages / context); aws-access-key-id (a.env); github-token ×2 (b.sh)"
	if got != want {
		t.Errorf("Summary =\n %q\nwant\n %q", got, want)
	}
	if Summary(nil) != nil {
		t.Error("empty summary should be nil")
	}
}

func TestHelpers(t *testing.T) {
	if entropy("") != 0 || entropy("aaaa") != 0 {
		t.Error("entropy of uniform string should be 0")
	}
	if e := entropy("abcd"); e != 2 {
		t.Errorf("entropy(abcd) = %v", e)
	}
	for _, f := range []string{".env", "prod.env", ".env.local", `C:\app\.env`} {
		if !isEnvFile(f) {
			t.Errorf("isEnvFile(%q) = false", f)
		}
	}
	for _, f := range []string{"", "env.go", "environment.ts"} {
		if isEnvFile(f) {
			t.Errorf("isEnvFile(%q) = true", f)
		}
	}
	if !isConfigFile("a/b/settings.TOML") || isConfigFile("main.go") || isConfigFile("") {
		t.Error("isConfigFile")
	}
	if s, e, q := parseValue(`"abc`, 0); s != 1 || e != 4 || !q {
		t.Errorf("unterminated quote: %d %d %v", s, e, q)
	}
	if s, _, _ := parseValue("x", 1); s != -1 {
		t.Error("value at end of line")
	}
	if s, _, _ := parseValue(" x", 0); s != -1 {
		t.Error("empty unquoted value")
	}
	if s, e, _ := parseValue(`"a\"b"`, 0); `"a\"b"`[s:e] != `a\"b` {
		t.Error("escaped quote")
	}
	r := New()
	r.MinEntropyLen = 100
	if _, fs := r.Diff(".env", "+X="+envRandom); len(fs) != 0 {
		t.Error("MinEntropyLen not honoured")
	}
	if got, _ := r.Diff(".env", "+TOKEN_A=Zk9qL2xW7vB4 # rotate monthly"); !strings.Contains(got, "# rotate monthly") { // gitleaks:allow (fake fixture)
		t.Errorf("trailing comment lost: %q", got)
	}
}

func TestCodeIdentifierHeuristic(t *testing.T) {
	r := New()
	for _, line := range []string{`+	apiKey = defaultAPIKeyV2`, `+	secret = cfg.Secret2024abc`} { // gitleaks:allow (fake fixture)
		if _, fs := r.Diff("x.go", line); len(fs) != 0 {
			t.Errorf("identifier treated as secret: %q", line)
		}
	}
}

func TestDocPlaceholders(t *testing.T) {
	r := New()
	for _, line := range []string{"+e.g. `postgres://user:pass@host`", "+DB_URL=mysql://root:password@localhost/db"} {
		if _, fs := r.Diff("README.md", line); len(fs) != 0 {
			t.Errorf("doc placeholder flagged: %q %+v", line, fs)
		}
	}
}
