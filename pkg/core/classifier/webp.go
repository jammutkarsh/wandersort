package classifier

type Webp struct {
	BlueMatrixColumn          string  `json:"BlueMatrixColumn"`
	BlueTRC                   string  `json:"BlueTRC"`
	CMMFlags                  int     `json:"CMMFlags"`
	ColorSpaceData            string  `json:"ColorSpaceData"`
	ConnectionSpaceIlluminant string  `json:"ConnectionSpaceIlluminant"`
	DeviceAttributes          string  `json:"DeviceAttributes"`
	DeviceManufacturer        string  `json:"DeviceManufacturer"`
	DeviceModel               string  `json:"DeviceModel"`
	Directory                 string  `json:"Directory"`
	ExifByteOrder             string  `json:"ExifByteOrder"`
	ExifToolVersion           float64 `json:"ExifToolVersion"`
	FileAccessDate            string  `json:"FileAccessDate"`
	FileInodeChangeDate       string  `json:"FileInodeChangeDate"`
	FileModifyDate            string  `json:"FileModifyDate"`
	FileName                  string  `json:"FileName"`
	FilePermissions           int     `json:"FilePermissions"`
	FileSize                  int     `json:"FileSize"`
	FileType                  string  `json:"FileType"`
	FileTypeExtension         string  `json:"FileTypeExtension"`
	GreenMatrixColumn         string  `json:"GreenMatrixColumn"`
	GreenTRC                  string  `json:"GreenTRC"`
	HorizontalScale           int     `json:"HorizontalScale"`
	ImageHeight               int     `json:"ImageHeight"`
	ImageSize                 string  `json:"ImageSize"`
	ImageWidth                int     `json:"ImageWidth"`
	LightSource               int     `json:"LightSource"`
	MIMEType                  string  `json:"MIMEType"`
	MediaWhitePoint           string  `json:"MediaWhitePoint"`
	Megapixels                float64 `json:"Megapixels"`
	ModifyDate                string  `json:"ModifyDate"`
	Orientation               int     `json:"Orientation"`
	PrimaryPlatform           string  `json:"PrimaryPlatform"`
	ProfileCMMType            string  `json:"ProfileCMMType"`
	ProfileClass              string  `json:"ProfileClass"`
	ProfileConnectionSpace    string  `json:"ProfileConnectionSpace"`
	ProfileCopyright          string  `json:"ProfileCopyright"`
	ProfileCreator            string  `json:"ProfileCreator"`
	ProfileDateTime           string  `json:"ProfileDateTime"`
	ProfileDescription        string  `json:"ProfileDescription"`
	ProfileFileSignature      string  `json:"ProfileFileSignature"`
	ProfileID                 string  `json:"ProfileID"`
	ProfileVersion            int     `json:"ProfileVersion"`
	RedMatrixColumn           string  `json:"RedMatrixColumn"`
	RedTRC                    string  `json:"RedTRC"`
	RenderingIntent           int     `json:"RenderingIntent"`
	SourceFile                string  `json:"SourceFile"`
	UserComment               string  `json:"UserComment"`
	VP8Version                int     `json:"VP8Version"`
	VerticalScale             int     `json:"VerticalScale"`
	Warning                   string  `json:"Warning"`
	WebPFlags                 int     `json:"WebP_Flags"`
}

func (Webp) MediaType() string { return MediaTypeImage }

func (w Webp) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:     ftoa(w.ExifToolVersion),
		SourceFile:          w.SourceFile,
		Directory:           w.Directory,
		FileName:            w.FileName,
		FileSize:            itoa(w.FileSize),
		FilePermissions:     itoa(w.FilePermissions),
		FileType:            w.FileType,
		FileTypeExtension:   w.FileTypeExtension,
		MIMEType:            w.MIMEType,
		FileModifyDate:      w.FileModifyDate,
		FileAccessDate:      w.FileAccessDate,
		FileInodeChangeDate: w.FileInodeChangeDate,
		ImageWidth:          itoa(w.ImageWidth),
		ImageHeight:         itoa(w.ImageHeight),
		ImageSize:           w.ImageSize,
		Megapixels:          ftoa(w.Megapixels),
		Orientation:         itoa(w.Orientation),
		ModifyDate:          w.ModifyDate,
	}
}
