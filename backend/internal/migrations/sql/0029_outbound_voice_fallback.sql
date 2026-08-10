CREATE TABLE human_calling_outbound_voice_fallbacks (
    practice_id uuid PRIMARY KEY REFERENCES access_practices(id),
    location_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (practice_id, location_id)
        REFERENCES access_locations(practice_id, id)
);
