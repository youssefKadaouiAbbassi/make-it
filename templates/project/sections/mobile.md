## Mobile

- Drive the app on a simulator or emulator with agent-device; run
  `agent-device doctor` once first. Start with
  `agent-device open <app> --platform ios` (or `android`), read the screen with
  `agent-device snapshot -i`, act on a ref from the latest output with
  `agent-device press @e2 --settle`, keep evidence with
  `agent-device screenshot <file>`, and end with `agent-device close`.
- Repeatable UI tests are Maestro flows in `.maestro/*.yaml` (`appId:`, then
  steps such as `launchApp`, `tapOn`, `assertVisible`), run with
  `maestro test .maestro/`. Maestro needs Java 17 or newer.
