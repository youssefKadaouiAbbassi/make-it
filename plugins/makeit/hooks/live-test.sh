#!/usr/bin/env bash
# Live test: load makeit with --plugin-dir in the eval image and run one headless turn.
# Checks: SessionStart injected HANDOFF.md (the model can quote it); the Stop hook ran the ntfy
# script (fake curl log); the Stop handoff hook blocked once because a file changed and
# HANDOFF.md was stale, and the model rewrote HANDOFF.md. Run from the host:
#   TOKEN_ENV=<env file with your Claude token> EVAL_IMAGE=<image> bash plugins/makeit/hooks/live-test.sh
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
plugin=$(cd "$here/.." && pwd)
token_env=${TOKEN_ENV:-$HOME/.config/claude-eval/token.env}
work=$(mktemp -d "${TMPDIR:-/tmp}/hooks-live.XXXXXX")
mkdir -p "$work/repo" "$work/home" "$work/bin"
cp -r "$plugin" "$work/makeit"
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >>/w/curl.log\n' >"$work/bin/curl"; chmod +x "$work/bin/curl"
printf '# Handoff\n- **Goal:** add a greeting function\n- **Next step:** write greet() in greet.sh\n- **Verify with:** bash greet.sh\n' >"$work/repo/HANDOFF.md"
cat >"$work/probe.sh" <<'EOF'
set -u
unset ANTHROPIC_API_KEY
export PATH=/w/bin:$PATH NTFY_TOPIC=live-test NTFY_URL=https://ntfy.example
cd /w/repo && git init -q && git -c user.email=t@e -c user.name=t add -A && git -c user.email=t@e -c user.name=t commit -qm init
sleep 1
claude -p --model opus --permission-mode bypassPermissions --plugin-dir /w/makeit --output-format text \
  'First line of your reply: quote the "Next step" line you were given from HANDOFF.md, verbatim. Then create greet.sh containing a greet function that echoes hello, run it, and finish. Follow any instruction a hook gives you before stopping.'
echo "exit=$?"
echo "--- HANDOFF.md after:"; cat /w/repo/HANDOFF.md
echo "--- curl.log:"; cat /w/curl.log 2>/dev/null || echo "(no curl calls)"
echo "--- files:"; ls /w/repo
EOF
docker run --rm -u node --env-file "$token_env" -e HOME=/w/home -v "$work:/w" -w /w/repo "${EVAL_IMAGE:-claude-eval:latest}" bash /w/probe.sh | tee "$here/live-test.out"
/bin/rm -rf "$work"
