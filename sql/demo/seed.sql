-- Demo fixtures (ADR-031). The canonical fixture set is the quiz-question
-- seed migration — re-including it here (idempotent by slug) instead of
-- copying it keeps one source of truth. Run via `task demo:seed`, which
-- refuses to run without DEMO_MODE=1.
--
-- Add demo-only fixtures below the include; anything a real deployment needs
-- belongs in a migration instead.

\ir ../../migrations/000007_seed_quiz_questions.up.sql
\ir ../../migrations/000008_seed_quiz_proof_questions.up.sql
