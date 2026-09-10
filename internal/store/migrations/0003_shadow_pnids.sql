-- A shadow PNID is the Wii U/3DS face of an OpenPak account that has no console of
-- this family: a Switch or phone user whom a Wii U friend list still has to show by
-- PID, name and Mii. It has no NEX account and no console link; it can never sign in.
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS shadow BOOLEAN NOT NULL DEFAULT FALSE;
