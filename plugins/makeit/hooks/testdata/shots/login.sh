# A setup file for shots, as a project writes one to log in: it is sourced once the browser is
# up, and calls the browser's command line through cdt.
cdt list_pages >"$SHOTS_TEST_MARK"
