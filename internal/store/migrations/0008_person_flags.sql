-- Profile fields a console sets with PUT /v1/api/people/@me and Pretendo keeps on the PNID.
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS gender TEXT NOT NULL DEFAULT '';
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS marketing_flag BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE pnids ADD COLUMN IF NOT EXISTS off_device_flag BOOLEAN NOT NULL DEFAULT FALSE;
