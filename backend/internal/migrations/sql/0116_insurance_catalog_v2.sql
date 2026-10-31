-- Rewrites every stored insurance list from the v1 catalog shape to v2 with the
-- conversion rules shared with the middleware: a carrier's code is the most
-- common non-empty code its plans carried (ties by lexical order), plans lose
-- carrierCode and selfPay, a row's requirements collapse to one beforeBooking
-- (staff_verify wins, else the first kind), notes are dropped, and the
-- hand-typed self-pay notice (trimmed of what Go's strings.TrimSpace trims) is
-- cleared so Abita decides for itself when to offer self-pay.
--
-- content_hash is the SHA-256 of Go's json.Marshal of the parsed catalog, so
-- insurance_go_json rebuilds those exact bytes: struct field order, map keys in
-- byte order, and Go's HTML-safe escaping of <, >, & and U+2028/U+2029.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '60s';

CREATE FUNCTION pg_temp.insurance_items(value jsonb)
RETURNS TABLE (item jsonb, place bigint)
LANGUAGE sql IMMUTABLE AS $$
    SELECT element, ordinality
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(value) = 'array' THEN value ELSE '[]'::jsonb END)
        WITH ORDINALITY AS elements(element, ordinality)
$$;

CREATE FUNCTION pg_temp.insurance_go_space()
RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT E' \t\n\v\f\r' || chr(133) || chr(160) || chr(5760)
        || chr(8192) || chr(8193) || chr(8194) || chr(8195) || chr(8196) || chr(8197) || chr(8198) || chr(8199) || chr(8200) || chr(8201) || chr(8202)
        || chr(8232) || chr(8233) || chr(8239) || chr(8287) || chr(12288)
$$;

CREATE FUNCTION pg_temp.insurance_catalog_v2(catalog jsonb)
RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    WITH codes AS (
        SELECT plan.item->>'carrierId' AS carrier, plan.item->>'carrierCode' AS code, count(*) AS uses
        FROM pg_temp.insurance_items(catalog->'regions') region,
            pg_temp.insurance_items(region.item->'plans') plan
        WHERE coalesce(plan.item->>'carrierCode', '') <> ''
        GROUP BY 1, 2
    )
    SELECT jsonb_build_object(
        'carriers', coalesce((
            SELECT jsonb_agg(carrier.item || jsonb_build_object('code', coalesce((
                SELECT codes.code FROM codes
                WHERE codes.carrier = carrier.item->>'id'
                ORDER BY codes.uses DESC, codes.code COLLATE "C"
                LIMIT 1
            ), '')) ORDER BY carrier.place)
            FROM pg_temp.insurance_items(catalog->'carriers') carrier
        ), '[]'::jsonb),
        'regions', coalesce((
            SELECT jsonb_agg(region.item || jsonb_build_object(
                'plans', coalesce((
                    SELECT jsonb_agg(plan.item - 'carrierCode' - 'selfPay' ORDER BY plan.place)
                    FROM pg_temp.insurance_items(region.item->'plans') plan
                ), '[]'::jsonb),
                'tables', coalesce((
                    SELECT jsonb_agg(office_table.item || jsonb_build_object('rows', coalesce((
                        SELECT jsonb_agg((plan_row.item - 'requires' - 'note') || jsonb_build_object(
                            'beforeBooking', CASE
                                WHEN NOT EXISTS (SELECT 1 FROM pg_temp.insurance_items(plan_row.item->'requires')) THEN 'none'
                                WHEN EXISTS (
                                    SELECT 1 FROM pg_temp.insurance_items(plan_row.item->'requires') requirement
                                    WHERE requirement.item->>'kind' = 'staff_verify'
                                ) THEN 'staff_verify'
                                ELSE (
                                    SELECT requirement.item->>'kind'
                                    FROM pg_temp.insurance_items(plan_row.item->'requires') requirement
                                    ORDER BY requirement.place
                                    LIMIT 1
                                )
                            END,
                            'notice', CASE
                                WHEN btrim(coalesce(plan_row.item->>'notice', ''), pg_temp.insurance_go_space()) = 'You can be seen as self-pay.' THEN ''
                                ELSE coalesce(plan_row.item->>'notice', '')
                            END
                        ) ORDER BY plan_row.place)
                        FROM pg_temp.insurance_items(office_table.item->'rows') plan_row
                    ), '[]'::jsonb)) ORDER BY office_table.place)
                    FROM pg_temp.insurance_items(region.item->'tables') office_table
                ), '[]'::jsonb)
            ) ORDER BY region.place)
            FROM pg_temp.insurance_items(catalog->'regions') region
        ), '[]'::jsonb)
    )
$$;

CREATE FUNCTION pg_temp.insurance_go_string(value jsonb)
RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN value IS NULL OR jsonb_typeof(value) = 'null' THEN '""'
        ELSE replace(replace(replace(replace(replace(value::text,
            '<', '\u003c'), '>', '\u003e'), '&', '\u0026'), chr(8232), '\u2028'), chr(8233), '\u2029')
    END
$$;

CREATE FUNCTION pg_temp.insurance_go_list(value jsonb, elements text)
RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(value) = 'array' THEN '[' || coalesce(elements, '') || ']' ELSE 'null' END
$$;

CREATE FUNCTION pg_temp.insurance_go_strings(value jsonb)
RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT pg_temp.insurance_go_list(value, (
        SELECT string_agg(pg_temp.insurance_go_string(entry.item), ',' ORDER BY entry.place)
        FROM pg_temp.insurance_items(value) entry
    ))
$$;

CREATE FUNCTION pg_temp.insurance_go_json(catalog jsonb)
RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT '{"carriers":' || pg_temp.insurance_go_list(catalog->'carriers', (
        SELECT string_agg(
            '{"id":' || pg_temp.insurance_go_string(carrier.item->'id')
            || ',"code":' || pg_temp.insurance_go_string(carrier.item->'code')
            || ',"name":' || pg_temp.insurance_go_string(carrier.item->'name') || '}',
            ',' ORDER BY carrier.place)
        FROM pg_temp.insurance_items(catalog->'carriers') carrier
    )) || ',"regions":' || pg_temp.insurance_go_list(catalog->'regions', (
        SELECT string_agg(
            '{"id":' || pg_temp.insurance_go_string(region.item->'id')
            || ',"plans":' || pg_temp.insurance_go_list(region.item->'plans', (
                SELECT string_agg(
                    '{"id":' || pg_temp.insurance_go_string(plan.item->'id')
                    || ',"label":' || pg_temp.insurance_go_string(plan.item->'label')
                    || ',"family":' || pg_temp.insurance_go_string(plan.item->'family')
                    || ',"coverage":' || pg_temp.insurance_go_string(plan.item->'coverage')
                    || ',"carrierId":' || pg_temp.insurance_go_string(plan.item->'carrierId')
                    || ',"aliases":' || pg_temp.insurance_go_strings(plan.item->'aliases') || '}',
                    ',' ORDER BY plan.place)
                FROM pg_temp.insurance_items(region.item->'plans') plan
            ))
            || ',"tables":' || pg_temp.insurance_go_list(region.item->'tables', (
                SELECT string_agg(
                    '{"id":' || pg_temp.insurance_go_string(office_table.item->'id')
                    || ',"offices":' || pg_temp.insurance_go_strings(office_table.item->'offices')
                    || ',"doctors":' || pg_temp.insurance_go_strings(office_table.item->'doctors')
                    || ',"rows":' || pg_temp.insurance_go_list(office_table.item->'rows', (
                        SELECT string_agg(
                            '{"planId":' || pg_temp.insurance_go_string(plan_row.item->'planId')
                            || ',"doctors":' || CASE
                                WHEN jsonb_typeof(plan_row.item->'doctors') = 'object' THEN '{' || coalesce((
                                    SELECT string_agg(
                                        pg_temp.insurance_go_string(to_jsonb(answer.key)) || ':' || pg_temp.insurance_go_string(answer.value),
                                        ',' ORDER BY answer.key COLLATE "C")
                                    FROM jsonb_each(plan_row.item->'doctors') answer
                                ), '') || '}'
                                ELSE 'null'
                            END
                            || ',"beforeBooking":' || pg_temp.insurance_go_string(plan_row.item->'beforeBooking')
                            || ',"onlyOffices":' || pg_temp.insurance_go_strings(plan_row.item->'onlyOffices')
                            || ',"notice":' || pg_temp.insurance_go_string(plan_row.item->'notice') || '}',
                            ',' ORDER BY plan_row.place)
                        FROM pg_temp.insurance_items(office_table.item->'rows') plan_row
                    )) || '}',
                    ',' ORDER BY office_table.place)
                FROM pg_temp.insurance_items(region.item->'tables') office_table
            )) || '}',
            ',' ORDER BY region.place)
        FROM pg_temp.insurance_items(catalog->'regions') region
    )) || '}'
$$;

UPDATE insurance_revisions
SET catalog = pg_temp.insurance_catalog_v2(catalog);

UPDATE insurance_revisions
SET content_hash = encode(sha256(convert_to(pg_temp.insurance_go_json(catalog), 'UTF8')), 'hex');
