-- Exif metadata on content groups (per unique hash)
ALTER TABLE content_groups ADD COLUMN exif_metadata JSONB;

-- Capture grouping: relate files from the same shutter press / capture event.
-- capture_stem is the normalised base name (e.g. "IMG_3162", "_MG_1721").
-- Together with source_root it forms a virtual capture group — no extra table needed.
ALTER TABLE file_registry ADD COLUMN capture_stem TEXT;
ALTER TABLE file_registry ADD COLUMN capture_role VARCHAR(20);

ALTER TABLE file_registry ADD CONSTRAINT valid_capture_role
    CHECK (capture_role IN (
        'ORIGINAL',          -- primary image  (HEIC, JPG, PNG …)
        'RAW',               -- camera raw     (CR2, DNG …)
        'LIVE_VIDEO',        -- live-photo video (MOV paired with HEIC)
        'SIDECAR',           -- edit sidecar   (AAE, XMP …)
        'EDITED',            -- device-edited image  (IMG_E prefix)
        'EDITED_VIDEO',      -- device-edited video  (IMG_E prefix + MOV)
        'ORIGINAL_SIDECAR'   -- original-state sidecar (IMG_O prefix + AAE)
    ));

-- Index for fast capture-group lookups
CREATE INDEX idx_file_registry_capture ON file_registry(capture_stem, source_root)
    WHERE capture_stem IS NOT NULL;