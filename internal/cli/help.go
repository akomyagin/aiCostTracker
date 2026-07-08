package cli

// adminKeyHelp is the shared UX text explaining that usage/cost APIs need an
// ADMIN/org-level key — not a normal model-calling API key — plus where to get
// one and how to supply it. It is embedded in --help (root and report) and, in a
// shorter form, appended to admin-key errors via adminKeyHint. Paths are taken
// from docs/API_NOTES.md (verified 2026-07-08).
const adminKeyHelp = "ADMIN KEY REQUIRED (this is NOT your model API key):\n" +
	"  The usage & cost endpoints are organization/admin APIs. The everyday key you\n" +
	"  use to call models (sk-ant-api... / a project sk-...) has NO access to them.\n" +
	"  You need a separate ADMIN key, issued by an organization owner:\n" +
	"\n" +
	"    Anthropic: console.anthropic.com -> Settings -> Organization -> Admin Keys\n" +
	"               (key looks like sk-ant-admin...)\n" +
	"    OpenAI:    platform.openai.com -> Settings -> Organization -> Admin Keys\n" +
	"               (key looks like sk-admin-...)\n" +
	"\n" +
	"  Supply it either via environment variables (recommended — never written to\n" +
	"  disk) or in the config file:\n" +
	"    export AICOST_ANTHROPIC_ADMIN_KEY=sk-ant-admin-...\n" +
	"    export AICOST_OPENAI_ADMIN_KEY=sk-admin-...\n" +
	"  Config file (~/.config/aicost/config.yaml on Linux; os.UserConfigDir()/aicost\n" +
	"  elsewhere) — set providers.<name>.admin_key. Env overrides the file.\n" +
	"\n" +
	"  Your admin key is never logged, printed, or included in any error message."

// adminKeyHint is the compact one-liner appended to admin-key-related errors so a
// user who hits an auth/missing-key failure is pointed at the right kind of key
// and where to get it, without dumping the full help block onto stderr.
const adminKeyHint = "hint: usage/cost APIs need an ADMIN/org key (NOT a model API key). " +
	"Anthropic: console.anthropic.com -> Organization -> Admin Keys (sk-ant-admin...); " +
	"OpenAI: platform.openai.com -> Organization -> Admin Keys (sk-admin-...). " +
	"Set AICOST_ANTHROPIC_ADMIN_KEY / AICOST_OPENAI_ADMIN_KEY or config.yaml."
