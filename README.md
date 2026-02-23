# WanderSort

**A local-first desktop application for organizing terabytes of chaotic media files.**

Inspired by [@WanderWithSky](https://drive.google.com/file/d/1QIDtm5rTkwzkQxyVaPqPN8J81vGuS-AF/view?usp=sharing)

## Philosophy

Scan → Deduce → Review → Execute

## Build Order

### Database + Basic Scanning

- [x] Set up PostgreSQL schema
- [x] Implement file_registry population
- [x] Test incremental scanning (add/remove files)

### Hashing + Deduplication

- [ ] BLAKE3 worker pool
- [ ] Content group creation
- [ ] Master selection logic

### Relationships + Metadata

- [ ] Sidecar file detection
- [ ] EXIF extraction (images)
- [ ] FFmpeg extraction (videos)

### Inference + Organization

- [ ] Peer inference waterfall
- [ ] Folder context analysis
- [ ] Virtual organization proposals

### Review UI + Execution

- [ ] React UI for approval
- [ ] Safe copy engine
- [ ] Verification logging
