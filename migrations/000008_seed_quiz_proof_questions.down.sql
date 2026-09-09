-- Remove the ADR-034 proof-surface questions (attempts/flashcards cascade
-- or null out per the FKs in 000003).
DELETE FROM quiz_questions WHERE slug IN (
    'observed-vs-budget',
    'isolation-check',
    'retry-after'
);
