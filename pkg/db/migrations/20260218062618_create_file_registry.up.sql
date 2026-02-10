-- Scan sessions table (tracks each scan operation)
CREATE TABLE IF NOT EXISTS scan_sessions (
    id UUID PRIMARY KEY,
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP,
    status VARCHAR(20) NOT NULL DEFAULT 'RUNNING',
    
    root_paths JSONB NOT NULL,
    
    -- Progress tracking
    files_discovered INT DEFAULT 0,
    files_skipped INT DEFAULT 0,     -- Unchanged files
    files_new INT DEFAULT 0,          -- New discoveries
    files_modified INT DEFAULT 0,     -- Changed since last scan
    
    -- Error tracking
    errors_encountered INT DEFAULT 0,
    last_error TEXT,
    
    CONSTRAINT valid_status CHECK (status IN ('RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED'))
);

CREATE INDEX idx_scan_sessions_status ON scan_sessions(status);
CREATE INDEX idx_scan_sessions_started ON scan_sessions(started_at DESC);


-- File registry table (the census of all files)
CREATE TABLE IF NOT EXISTS file_registry (
    id BIGSERIAL PRIMARY KEY,
    
    -- Physical identity
    file_path TEXT NOT NULL,
    file_size BIGINT NOT NULL,
    file_modified_at TIMESTAMP NOT NULL,
    
    -- Hash will be populated in Step 3 (hashing phase)
    file_hash CHAR(64),  -- Nullable initially
    
    -- Discovery metadata
    discovered_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMP NOT NULL DEFAULT NOW(),  -- Updated each scan
    scan_session_id UUID NOT NULL REFERENCES scan_sessions(id),
    source_root TEXT NOT NULL,  -- Which root path this came from
    
    -- File classification
    media_type VARCHAR(20),  -- IMAGE, VIDEO, SIDECAR, RAW, UNKNOWN
    file_extension VARCHAR(10) NOT NULL,
    
    -- Processing state machine
    scan_status VARCHAR(20) NOT NULL DEFAULT 'DISCOVERED',
    
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    CONSTRAINT valid_media_type CHECK (media_type IN ('IMAGE', 'VIDEO', 'SIDECAR', 'RAW', 'UNKNOWN')),
    CONSTRAINT valid_scan_status CHECK (scan_status IN ('DISCOVERED', 'HASHING', 'HASHED', 'ANALYZING', 'ANALYZED', 'ERROR'))
);

-- Indexes for performance
CREATE UNIQUE INDEX idx_file_registry_path ON file_registry(file_path);
CREATE INDEX idx_file_registry_hash ON file_registry(file_hash) WHERE file_hash IS NOT NULL;
CREATE INDEX idx_file_registry_session ON file_registry(scan_session_id);
CREATE INDEX idx_file_registry_status ON file_registry(scan_status);
CREATE INDEX idx_file_registry_source_root ON file_registry(source_root);
CREATE INDEX idx_file_registry_media_type ON file_registry(media_type);

-- Trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_file_registry_updated_at
    BEFORE UPDATE ON file_registry
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();