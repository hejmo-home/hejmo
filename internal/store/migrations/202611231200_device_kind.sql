-- The kind of device (#243): a pairing stores the kind of screen (phone, tablet, device) of the
-- personal device that opened its code as its own column, and the browser and device as keys
-- ("safari|iphone", like a browser session's), so the label can be translated when it is shown
-- and nothing has to read the kind back from text. Pairings opened before this migration carry
-- the English label ("Safari on iPhone"): kind and keys are derived from it once, here.
ALTER TABLE personal_device_pairings ADD COLUMN device_kind TEXT NOT NULL DEFAULT 'device';

UPDATE personal_device_pairings SET
    device_kind = CASE
        WHEN device LIKE '% on iPhone' OR device LIKE '% on Android phone' THEN 'phone'
        WHEN device LIKE '% on iPad' OR device LIKE '% on Android tablet' THEN 'tablet'
        ELSE 'device' END,
    device = CASE WHEN device LIKE 'Firefox%' THEN 'firefox'
                  WHEN device LIKE 'Edge%' THEN 'edge'
                  WHEN device LIKE 'Samsung Internet%' THEN 'samsung'
                  WHEN device LIKE 'Chrome%' THEN 'chrome'
                  WHEN device LIKE 'Safari%' THEN 'safari'
                  ELSE 'unknown' END
        || '|' ||
        CASE WHEN device LIKE '% on iPhone' THEN 'iphone'
             WHEN device LIKE '% on Android phone' THEN 'android_phone'
             WHEN device LIKE '% on iPad' THEN 'ipad'
             WHEN device LIKE '% on Android tablet' THEN 'android_tablet'
             ELSE 'unknown' END
WHERE device <> '';
