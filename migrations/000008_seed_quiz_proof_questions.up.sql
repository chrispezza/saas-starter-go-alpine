-- Seed quiz questions for the ADR-034 proof surfaces, so the explainer's
-- promise ("what you read here is what you'll be asked") stays true after
-- the landing grid, the isolation check and the rate-limit demo landed.
-- Same idempotent-by-slug shape as 000007; re-included by sql/demo/seed.sql.

INSERT INTO quiz_questions (slug, topic, prompt, choices, correct_index, explanation) VALUES
(
    'observed-vs-budget',
    'performance',
    'On the landing page''s budget grid, where does the "observed" column come from?',
    '["This process: a rolling window of request durations, runtime memory, the startup time main recorded, and the shipped assets measured on disk", "A hardcoded list updated at release time", "A Lighthouse run in CI", "The Fly.io dashboard API"]',
    0,
    'internal/performance keeps an Observer the metrics middleware feeds on every request; main records process start → listening once the socket is bound. The handler reads a snapshot and renders it next to the ADR-000 constants — a zero reading renders as "not measured", never as a passing value (ADR-034).'
),
(
    'isolation-check',
    'database',
    'The flashcards isolation check runs your own query as a "stranger". What changes between the two reads?',
    '["Only the JWT claims in the transaction — same SQL, same user_id parameter, a freshly minted auth.uid()", "The WHERE clause is removed", "It reads another visitor''s rows under service_role", "The query switches to a different table"]',
    0,
    'Both reads go through the same repository method with the same user_id; the second carries a random authenticated identity that owns no users row, so the flashcards_self_access policy returns nothing. Zero rows is Postgres refusing, not application code filtering — and no other visitor''s data is touched (ADR-004, ADR-034).'
),
(
    'retry-after',
    'routing',
    'When the rate limiter refuses a request, what does the client get?',
    '["A 429 with a Retry-After header in whole seconds, and the refused request costs no token", "A 503 and a silent retry", "A 200 with an empty body", "The request is queued until a token frees up"]',
    0,
    'The limiter reserves a token, and if the bucket needs time it cancels the reservation and answers 429 with Retry-After rounded up from the refill delay (never 0). The /patterns demo mounts the same middleware on a tight tier with an HTML refusal so you can watch it say no (ADR-014, ADR-034).'
)
ON CONFLICT (slug) DO NOTHING;
