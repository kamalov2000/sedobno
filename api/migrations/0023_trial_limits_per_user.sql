-- +goose Up
-- Потолки триала, ЗАФИКСИРОВАННЫЕ за человеком.
--
-- Срок недели у каждого свой (users.trial_expires_at) и правка TRIAL_DAYS его не двигает:
-- условия начатого периода не ухудшаются. С потолками разборов было иначе — они читались
-- из окружения на КАЖДЫЙ запрос, и значение, уменьшенное ради экономики, мгновенно
-- закрывало доступ людям в середине их недели: человек, сделавший 20 разборов из 30,
-- при новом потолке 15 получал пейволл, ничего не нарушив. Это ровно то ухудшение
-- условий, которого не должно быть.
--
-- Теперь потолок берётся из строки пользователя. Новым он проставляется при регистрации
-- из текущей настройки; существующим — заполняется действующими сегодня значениями
-- (30/30), то есть их условия остаются буквально теми же, что были обещаны.
ALTER TABLE users ADD COLUMN trial_meal_limit INT;
ALTER TABLE users ADD COLUMN trial_scan_limit INT;

UPDATE users
   SET trial_meal_limit = COALESCE(trial_meal_limit, 30),
       trial_scan_limit = COALESCE(trial_scan_limit, 30);

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS trial_scan_limit;
ALTER TABLE users DROP COLUMN IF EXISTS trial_meal_limit;
