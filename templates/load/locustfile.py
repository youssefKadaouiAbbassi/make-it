"""Load test template with a threshold hook (Locust 2.46.7, MIT).

Scenarios are plain Python here; thresholds come from CONSTRAINTS.md through environment
variables so the file never carries a number of its own. Headless run:

    LOCUST_P95_MS=500 LOCUST_FAIL_RATIO=0.01 \
    locust -f locustfile.py --headless -u 20 -r 5 -t 2m --host https://staging.example.com

The quitting listener sets the process exit code (Locust docs, "Controlling the exit code of
the Locust process"), so CI fails when the p95 or the failure ratio is over the threshold.
"""

import logging
import os

from locust import HttpUser, between, events, task

P95_MS = float(os.environ.get("LOCUST_P95_MS", "500"))
FAIL_RATIO = float(os.environ.get("LOCUST_FAIL_RATIO", "0.01"))


class ReadUser(HttpUser):
    wait_time = between(0.5, 2)

    @task(3)
    def home(self) -> None:
        self.client.get("/")

    @task
    def health(self) -> None:
        self.client.get("/health")


@events.quitting.add_listener
def enforce_thresholds(environment, **_kwargs) -> None:
    total = environment.stats.total
    p95 = total.get_response_time_percentile(0.95) or 0
    if total.fail_ratio > FAIL_RATIO:
        logging.error("threshold: fail ratio %.3f > %.3f", total.fail_ratio, FAIL_RATIO)
        environment.process_exit_code = 1
    elif p95 > P95_MS:
        logging.error("threshold: p95 %.0f ms > %.0f ms", p95, P95_MS)
        environment.process_exit_code = 1
    else:
        environment.process_exit_code = 0
