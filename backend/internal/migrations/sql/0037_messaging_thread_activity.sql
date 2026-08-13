-- Map Call and phone-only Task activity to Message Threads without scanning
-- every office-phone branch in a Location.
CREATE INDEX messaging_threads_phone_activity_idx
    ON messaging_threads (practice_id, location_id, external_phone, id);
