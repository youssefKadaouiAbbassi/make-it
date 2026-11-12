# A setup file for shots, as a project writes one: sourced once the browser is up, it signs in
# through cdt, the browser's command line. The pages shots opens next share that session.
login=$(cdt new_page "http://127.0.0.1:$SHOTS_TEST_PORT/login" --output-format=json | jq -r '[.pages[] | select(.selected) | .id][0]')
cdt evaluate_script '() => { document.querySelector("[name=user]").value = "dev"; document.querySelector("form").submit(); }' --pageId "$login" >/dev/null
