-- GENERATED (one-off script, not in repo). Team names from docs/data/euroleague_season.csv;
-- all results synthetic (seeded RNG). Single round-robin minus the last round:
-- 8 rounds x 5 matches, every team plays exactly once per round, no pairing repeats.
-- Rounds 1-4: closed (past, fake results). Round 5: open (first upcoming). Rounds 6-8: created.
-- IDs are deterministic via insert order + TRUNCATE ... RESTART IDENTITY in cmd/seed.

INSERT INTO teams (name, logo, description) VALUES
	('Anadolu Efes', 'https://x.example/anadolu-efes.png', 'Istanbul, Turkey'),
	('Barcelona', 'https://x.example/barcelona.png', 'Barcelona, Spain'),
	('Dubai', 'https://x.example/dubai.png', 'Dubai, UAE'),
	('Fenerbahce', 'https://x.example/fenerbahce.png', 'Istanbul, Turkey'),
	('Maccabi Tel Aviv', 'https://x.example/maccabi-tel-aviv.png', 'Tel Aviv, Israel'),
	('Monaco', 'https://x.example/monaco.png', 'Monaco'),
	('Olympiacos', 'https://x.example/olympiacos.png', 'Piraeus, Greece'),
	('Panathinaikos', 'https://x.example/panathinaikos.png', 'Athens, Greece'),
	('Real Madrid', 'https://x.example/real-madrid.png', 'Madrid, Spain'),
	('Zalgiris Kaunas', 'https://x.example/zalgiris-kaunas.png', 'Kaunas, Lithuania');

INSERT INTO seasons (status) VALUES ('in_progress'); -- id 1

INSERT INTO rounds (season_id, number, status) VALUES
	(1, 1, 'closed'),
	(1, 2, 'closed'),
	(1, 3, 'closed'),
	(1, 4, 'closed'),
	(1, 5, 'open'),
	(1, 6, 'created'),
	(1, 7, 'created'),
	(1, 8, 'created');

INSERT INTO matches (
	season_id, round_id, home_team_id, away_team_id,
	home_odds, away_odds, home_score, away_score, status, starts_at
) VALUES
	(1, 1, 1, 10, 2.73, 1.79, 86, 92, 'closed', now() - interval '28 days'),
	(1, 1, 2, 9, 1.88, 3.93, 101, 97, 'closed', now() - interval '28 days'),
	(1, 1, 3, 8, 1.28, 2.34, 91, 77, 'closed', now() - interval '28 days'),
	(1, 1, 4, 7, 3.85, 1.73, 92, 106, 'closed', now() - interval '28 days'),
	(1, 1, 5, 6, 1.63, 3.0, 84, 79, 'closed', now() - interval '28 days'),
	(1, 2, 1, 9, 2.7, 1.54, 90, 94, 'closed', now() - interval '21 days'),
	(1, 2, 10, 8, 1.33, 3.67, 103, 96, 'closed', now() - interval '21 days'),
	(1, 2, 2, 7, 1.87, 3.68, 96, 92, 'closed', now() - interval '21 days'),
	(1, 2, 3, 6, 3.9, 1.95, 78, 95, 'closed', now() - interval '21 days'),
	(1, 2, 4, 5, 1.31, 3.33, 93, 89, 'closed', now() - interval '21 days'),
	(1, 3, 1, 8, 1.99, 3.54, 98, 77, 'closed', now() - interval '14 days'),
	(1, 3, 9, 7, 1.39, 2.89, 105, 98, 'closed', now() - interval '14 days'),
	(1, 3, 10, 6, 1.83, 2.85, 104, 99, 'closed', now() - interval '14 days'),
	(1, 3, 2, 5, 1.46, 3.15, 92, 71, 'closed', now() - interval '14 days'),
	(1, 3, 3, 4, 1.44, 2.89, 85, 79, 'closed', now() - interval '14 days'),
	(1, 4, 1, 7, 1.93, 3.51, 86, 80, 'closed', now() - interval '7 days'),
	(1, 4, 8, 6, 3.85, 1.73, 69, 90, 'closed', now() - interval '7 days'),
	(1, 4, 9, 5, 3.99, 2.0, 81, 88, 'closed', now() - interval '7 days'),
	(1, 4, 10, 4, 1.88, 3.7, 102, 92, 'closed', now() - interval '7 days'),
	(1, 4, 2, 3, 2.86, 1.59, 74, 88, 'closed', now() - interval '7 days'),
	(1, 5, 1, 6, 3.03, 4.97, NULL, NULL, 'open', now() + interval '2 days 0 hours'),
	(1, 5, 7, 5, 1.61, 2.21, NULL, NULL, 'open', now() + interval '2 days 2 hours'),
	(1, 5, 8, 4, 2.85, 4.28, NULL, NULL, 'open', now() + interval '2 days 4 hours'),
	(1, 5, 9, 3, 2.15, 3.58, NULL, NULL, 'open', now() + interval '2 days 6 hours'),
	(1, 5, 10, 2, 2.35, 3.13, NULL, NULL, 'open', now() + interval '2 days 8 hours'),
	(1, 6, 1, 5, 2.55, 4.64, NULL, NULL, 'created', now() + interval '9 days 0 hours'),
	(1, 6, 6, 4, 2.85, 3.37, NULL, NULL, 'created', now() + interval '9 days 2 hours'),
	(1, 6, 7, 3, 3.33, 5.06, NULL, NULL, 'created', now() + interval '9 days 4 hours'),
	(1, 6, 8, 2, 3.06, 4.01, NULL, NULL, 'created', now() + interval '9 days 6 hours'),
	(1, 6, 9, 10, 1.94, 2.54, NULL, NULL, 'created', now() + interval '9 days 8 hours'),
	(1, 7, 1, 4, 1.26, 2.93, NULL, NULL, 'created', now() + interval '16 days 0 hours'),
	(1, 7, 5, 3, 2.94, 5.09, NULL, NULL, 'created', now() + interval '16 days 2 hours'),
	(1, 7, 6, 2, 3.04, 4.3, NULL, NULL, 'created', now() + interval '16 days 4 hours'),
	(1, 7, 7, 10, 1.5, 2.99, NULL, NULL, 'created', now() + interval '16 days 6 hours'),
	(1, 7, 8, 9, 3.23, 4.49, NULL, NULL, 'created', now() + interval '16 days 8 hours'),
	(1, 8, 1, 3, 1.72, 2.73, NULL, NULL, 'created', now() + interval '23 days 0 hours'),
	(1, 8, 4, 2, 1.63, 3.74, NULL, NULL, 'created', now() + interval '23 days 2 hours'),
	(1, 8, 5, 10, 3.42, 5.47, NULL, NULL, 'created', now() + interval '23 days 4 hours'),
	(1, 8, 6, 9, 2.66, 3.89, NULL, NULL, 'created', now() + interval '23 days 6 hours'),
	(1, 8, 7, 8, 1.51, 2.5, NULL, NULL, 'created', now() + interval '23 days 8 hours');

-- 20 registered users (game players, not real athletes), 2 favorite fans per team.
INSERT INTO players (season_id, favorite_team_id, name) VALUES
	(1, 7, 'hoops_hawk_helen'),
	(1, 7, 'piraeus_pete'),
	(1, 9, 'pickmaster_paula'),
	(1, 9, 'merengue_max'),
	(1, 4, 'three_pointer_tina'),
	(1, 4, 'istanbul_iva'),
	(1, 2, 'bank_shot_boris'),
	(1, 2, 'catalan_carla'),
	(1, 10, 'streaker_stefan'),
	(1, 10, 'kaunas_kaspars'),
	(1, 8, 'green_gretha'),
	(1, 8, 'athens_ari'),
	(1, 3, 'desert_dmitri'),
	(1, 3, 'dune_drifter'),
	(1, 1, 'efes_elif'),
	(1, 1, 'navy_nika'),
	(1, 6, 'monaco_mika'),
	(1, 6, 'riviera_ray'),
	(1, 5, 'maccabi_moshe'),
	(1, 5, 'telaviv_tova');
