# hello-stack

Test fixture for AgentBox: a dev server on port 3000 and Postgres on port 5432.

1. If there is no `.env`, copy `.env.example` to `.env`.
2. Start Postgres: `docker compose up -d --wait`
3. Start the dev server: `pnpm dev`
4. Run the tests: `npm test` (writes `test-results/junit.xml` and an HTML report in `test-report/`)
