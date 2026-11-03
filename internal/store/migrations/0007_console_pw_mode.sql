-- Where the console credential came from: 'sync' (the OpenPak password, set at website sign-in),
-- 'custom' (a separate console password the player chose because the OpenPak password cannot
-- be typed on a console), 'incompatible' (the OpenPak password cannot be typed on a console and
-- no separate one is set; the credential is random until one is), '' (never set by the website).
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS console_pw_mode TEXT NOT NULL DEFAULT '';
