package classifier

type Bmp struct {
	BMPVersion          int     `json:"BMPVersion"`
	BitDepth            int     `json:"BitDepth"`
	Compression         int     `json:"Compression"`
	Directory           string  `json:"Directory"`
	ExifToolVersion     float64 `json:"ExifToolVersion"`
	FileAccessDate      string  `json:"FileAccessDate"`
	FileInodeChangeDate string  `json:"FileInodeChangeDate"`
	FileModifyDate      string  `json:"FileModifyDate"`
	FileName            string  `json:"FileName"`
	FilePermissions     int     `json:"FilePermissions"`
	FileSize            int     `json:"FileSize"`
	FileType            string  `json:"FileType"`
	FileTypeExtension   string  `json:"FileTypeExtension"`
	ImageHeight         int     `json:"ImageHeight"`
	ImageLength         int     `json:"ImageLength"`
	ImageSize           string  `json:"ImageSize"`
	ImageWidth          int     `json:"ImageWidth"`
	MIMEType            string  `json:"MIMEType"`
	Megapixels          float64 `json:"Megapixels"`
	NumColors           int     `json:"NumColors"`
	NumImportantColors  int     `json:"NumImportantColors"`
	PixelsPerMeterX     int     `json:"PixelsPerMeterX"`
	PixelsPerMeterY     int     `json:"PixelsPerMeterY"`
	Planes              int     `json:"Planes"`
	SourceFile          string  `json:"SourceFile"`
}

func (Bmp) MediaType() string { return MediaTypeImage }

func (b Bmp) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:     ftoa(b.ExifToolVersion),
		SourceFile:          b.SourceFile,
		Directory:           b.Directory,
		FileName:            b.FileName,
		FileSize:            itoa(b.FileSize),
		FilePermissions:     itoa(b.FilePermissions),
		FileType:            b.FileType,
		FileTypeExtension:   b.FileTypeExtension,
		MIMEType:            b.MIMEType,
		FileModifyDate:      b.FileModifyDate,
		FileAccessDate:      b.FileAccessDate,
		FileInodeChangeDate: b.FileInodeChangeDate,
		ImageWidth:          itoa(b.ImageWidth),
		ImageHeight:         itoa(b.ImageHeight),
		ImageSize:           b.ImageSize,
		Megapixels:          ftoa(b.Megapixels),
	}
}
