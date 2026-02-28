package classifier

import "time"

type Png struct {
	AEAverage                         int       `json:"AEAverage"`
	AEStable                          int       `json:"AEStable"`
	AETarget                          int       `json:"AETarget"`
	AFStable                          int       `json:"AFStable"`
	AccelerationVector                string    `json:"AccelerationVector"`
	Aperture                          float64   `json:"Aperture"`
	ApertureValue                     float64   `json:"ApertureValue"`
	AppleDataOffsets                  string    `json:"AppleDataOffsets"`
	ArtworkTitle                      string    `json:"ArtworkTitle"`
	BackgroundColor                   string    `json:"BackgroundColor"`
	BitDepth                          int       `json:"BitDepth"`
	BitsPerSample                     int       `json:"BitsPerSample"`
	BlueMatrixColumn                  string    `json:"BlueMatrixColumn"`
	BlueTRC                           string    `json:"BlueTRC"`
	BlueX                             float64   `json:"BlueX"`
	BlueY                             float64   `json:"BlueY"`
	BrightnessValue                   float64   `json:"BrightnessValue"`
	CMMFlags                          int       `json:"CMMFlags"`
	ChromaticAdaptation               string    `json:"ChromaticAdaptation"`
	CircleOfConfusion                 float64   `json:"CircleOfConfusion"`
	ColorComponents                   int       `json:"ColorComponents"`
	ColorPrimaries                    int       `json:"ColorPrimaries"`
	ColorSpace                        int       `json:"ColorSpace"`
	ColorSpaceData                    string    `json:"ColorSpaceData"`
	ColorType                         int       `json:"ColorType"`
	ComponentsConfiguration           []int     `json:"ComponentsConfiguration"`
	CompositeImage                    int       `json:"CompositeImage"`
	CompositeImageCount               string    `json:"CompositeImageCount"`
	Compression                       int       `json:"Compression"`
	ConnectionSpaceIlluminant         string    `json:"ConnectionSpaceIlluminant"`
	CreateDate                        string    `json:"CreateDate"`
	CreatorTool                       string    `json:"CreatorTool"`
	CurrentIPTCDigest                 string    `json:"CurrentIPTCDigest"`
	CustomRendered                    int       `json:"CustomRendered"`
	DateCreated                       string    `json:"DateCreated"`
	DateTimeOriginal                  string    `json:"DateTimeOriginal"`
	Datecreate                        time.Time `json:"Datecreate"`
	Datemodify                        time.Time `json:"Datemodify"`
	Datetimestamp                     time.Time `json:"Datetimestamp"`
	DerivedFromDocumentID             string    `json:"DerivedFromDocumentID"`
	DerivedFromInstanceID             string    `json:"DerivedFromInstanceID"`
	Description                       string    `json:"Description"`
	DeviceAttributes                  string    `json:"DeviceAttributes"`
	DeviceManufacturer                string    `json:"DeviceManufacturer"`
	DeviceModel                       string    `json:"DeviceModel"`
	DigitalZoomRatio                  float64   `json:"DigitalZoomRatio"`
	Directory                         string    `json:"Directory"`
	DocumentID                        string    `json:"DocumentID"`
	EncodingProcess                   int       `json:"EncodingProcess"`
	ExifByteOrder                     string    `json:"ExifByteOrder"`
	ExifImageHeight                   int       `json:"ExifImageHeight"`
	ExifImageWidth                    int       `json:"ExifImageWidth"`
	ExifToolVersion                   float64   `json:"ExifToolVersion"`
	ExifVersion                       string    `json:"ExifVersion"`
	ExposureCompensation              int       `json:"ExposureCompensation"`
	ExposureMode                      int       `json:"ExposureMode"`
	ExposureProgram                   int       `json:"ExposureProgram"`
	ExposureTime                      float64   `json:"ExposureTime"`
	FNumber                           float64   `json:"FNumber"`
	Fov                               float64   `json:"FOV"`
	FileAccessDate                    string    `json:"FileAccessDate"`
	FileInodeChangeDate               string    `json:"FileInodeChangeDate"`
	FileModifyDate                    string    `json:"FileModifyDate"`
	FileName                          string    `json:"FileName"`
	FilePermissions                   int       `json:"FilePermissions"`
	FileSize                          int       `json:"FileSize"`
	FileType                          string    `json:"FileType"`
	FileTypeExtension                 string    `json:"FileTypeExtension"`
	Filter                            int       `json:"Filter"`
	Firmware                          string    `json:"Firmware"`
	Flash                             int       `json:"Flash"`
	FlashCompensation                 int       `json:"FlashCompensation"`
	FlashFired                        bool      `json:"FlashFired"`
	FlashFunction                     bool      `json:"FlashFunction"`
	FlashMode                         int       `json:"FlashMode"`
	FlashPixVersion                   string    `json:"FlashPixVersion"`
	FlashRedEyeMode                   bool      `json:"FlashRedEyeMode"`
	FlashReturn                       int       `json:"FlashReturn"`
	FocalLenIn35MmFilm                int       `json:"FocalLenIn35mmFilm"`
	FocalLength                       float64   `json:"FocalLength"`
	FocalLength35Efl                  int       `json:"FocalLength35efl"`
	FocalLengthIn35MmFormat           int       `json:"FocalLengthIn35mmFormat"`
	FocalPlaneResolutionUnit          int       `json:"FocalPlaneResolutionUnit"`
	FocalPlaneXResolution             float64   `json:"FocalPlaneXResolution"`
	FocalPlaneYResolution             float64   `json:"FocalPlaneYResolution"`
	FocusDistanceRange                string    `json:"FocusDistanceRange"`
	GPSDateStamp                      string    `json:"GPSDateStamp"`
	GPSDateTime                       string    `json:"GPSDateTime"`
	GPSLatitude                       float64   `json:"GPSLatitude"`
	GPSLatitudeRef                    string    `json:"GPSLatitudeRef"`
	GPSLongitude                      float64   `json:"GPSLongitude"`
	GPSLongitudeRef                   string    `json:"GPSLongitudeRef"`
	GPSPosition                       string    `json:"GPSPosition"`
	GPSTimeStamp                      string    `json:"GPSTimeStamp"`
	Gamma                             float64   `json:"Gamma"`
	GreenMatrixColumn                 string    `json:"GreenMatrixColumn"`
	GreenTRC                          string    `json:"GreenTRC"`
	GreenX                            float64   `json:"GreenX"`
	GreenY                            float64   `json:"GreenY"`
	HDRImageType                      int       `json:"HDRImageType"`
	HorizontalScale                   int       `json:"HorizontalScale"`
	HostComputer                      string    `json:"HostComputer"`
	HyperfocalDistance                float64   `json:"HyperfocalDistance"`
	IPTCDigest                        string    `json:"IPTCDigest"`
	Iso                               int       `json:"ISO"`
	Icccopyright                      string    `json:"Icccopyright"`
	Iccdescription                    string    `json:"Iccdescription"`
	ImageDescription                  string    `json:"ImageDescription"`
	ImageHeight                       int       `json:"ImageHeight"`
	ImageSize                         string    `json:"ImageSize"`
	ImageUniqueID                     string    `json:"ImageUniqueID"`
	ImageWidth                        int       `json:"ImageWidth"`
	InstanceID                        string    `json:"InstanceID"`
	Interlace                         int       `json:"Interlace"`
	JFIFVersion                       string    `json:"JFIFVersion"`
	Lens                              string    `json:"Lens"`
	LensID                            string    `json:"LensID"`
	LensInfo                          string    `json:"LensInfo"`
	LensMake                          string    `json:"LensMake"`
	LensModel                         string    `json:"LensModel"`
	LensSerialNumber                  string    `json:"LensSerialNumber"`
	LightValue                        float64   `json:"LightValue"`
	MIMEType                          string    `json:"MIMEType"`
	Make                              string    `json:"Make"`
	MakerNoteVersion                  int       `json:"MakerNoteVersion"`
	MatrixCoefficients                int       `json:"MatrixCoefficients"`
	MaxApertureValue                  float64   `json:"MaxApertureValue"`
	MediaBlackPoint                   string    `json:"MediaBlackPoint"`
	MediaWhitePoint                   string    `json:"MediaWhitePoint"`
	Megapixels                        float64   `json:"Megapixels"`
	MeteringMode                      int       `json:"MeteringMode"`
	Model                             string    `json:"Model"`
	ModifyDate                        string    `json:"ModifyDate"`
	OISMode                           int       `json:"OISMode"`
	OffsetTime                        string    `json:"OffsetTime"`
	OffsetTimeDigitized               string    `json:"OffsetTimeDigitized"`
	OffsetTimeOriginal                string    `json:"OffsetTimeOriginal"`
	Orientation                       int       `json:"Orientation"`
	OriginalDocumentID                string    `json:"OriginalDocumentID"`
	OwnerName                         string    `json:"OwnerName"`
	Palette                           string    `json:"Palette"`
	PdfHiResBoundingBox               string    `json:"PdfHiResBoundingBox"`
	PdfVersion                        string    `json:"PdfVersion"`
	PhotographicSensitivity           int       `json:"PhotographicSensitivity"`
	PhotometricInterpretation         int       `json:"PhotometricInterpretation"`
	PixelUnits                        int       `json:"PixelUnits"`
	PixelsPerUnitX                    int       `json:"PixelsPerUnitX"`
	PixelsPerUnitY                    int       `json:"PixelsPerUnitY"`
	PrimaryPlatform                   string    `json:"PrimaryPlatform"`
	ProfileCMMType                    string    `json:"ProfileCMMType"`
	ProfileClass                      string    `json:"ProfileClass"`
	ProfileConnectionSpace            string    `json:"ProfileConnectionSpace"`
	ProfileCopyright                  string    `json:"ProfileCopyright"`
	ProfileCreator                    string    `json:"ProfileCreator"`
	ProfileDateTime                   string    `json:"ProfileDateTime"`
	ProfileDescription                string    `json:"ProfileDescription"`
	ProfileFileSignature              string    `json:"ProfileFileSignature"`
	ProfileID                         string    `json:"ProfileID"`
	ProfileName                       string    `json:"ProfileName"`
	ProfileVersion                    int       `json:"ProfileVersion"`
	Rating                            int       `json:"Rating"`
	RecommendedExposureIndex          int       `json:"RecommendedExposureIndex"`
	RedMatrixColumn                   string    `json:"RedMatrixColumn"`
	RedTRC                            string    `json:"RedTRC"`
	RedX                              float64   `json:"RedX"`
	RedY                              float64   `json:"RedY"`
	RenderingIntent                   int       `json:"RenderingIntent"`
	ResolutionUnit                    int       `json:"ResolutionUnit"`
	RunTimeEpoch                      int       `json:"RunTimeEpoch"`
	RunTimeFlags                      int       `json:"RunTimeFlags"`
	RunTimeScale                      int       `json:"RunTimeScale"`
	RunTimeSincePowerUp               float64   `json:"RunTimeSincePowerUp"`
	RunTimeValue                      int64     `json:"RunTimeValue"`
	SRGBRendering                     int       `json:"SRGBRendering"`
	ScaleFactor35Efl                  float64   `json:"ScaleFactor35efl"`
	SceneCaptureType                  int       `json:"SceneCaptureType"`
	SceneType                         int       `json:"SceneType"`
	SensingMethod                     int       `json:"SensingMethod"`
	SensitivityType                   int       `json:"SensitivityType"`
	SerialNumber                      string    `json:"SerialNumber"`
	ShutterSpeed                      float64   `json:"ShutterSpeed"`
	ShutterSpeedValue                 float64   `json:"ShutterSpeedValue"`
	SignificantBits                   string    `json:"SignificantBits"`
	Software                          string    `json:"Software"`
	SourceFile                        string    `json:"SourceFile"`
	SourceImageNumberOfCompositeImage string    `json:"SourceImageNumberOfCompositeImage"`
	SubSecCreateDate                  string    `json:"SubSecCreateDate"`
	SubSecDateTimeOriginal            string    `json:"SubSecDateTimeOriginal"`
	SubSecModifyDate                  string    `json:"SubSecModifyDate"`
	SubSecTimeDigitized               int       `json:"SubSecTimeDigitized"`
	SubSecTimeOriginal                int       `json:"SubSecTimeOriginal"`
	SubsecTime                        int       `json:"SubsecTime"`
	SubsecTimeDigitized               int       `json:"SubsecTimeDigitized"`
	SubsecTimeOriginal                int       `json:"SubsecTimeOriginal"`
	ThumbnailImage                    string    `json:"ThumbnailImage"`
	ThumbnailLength                   int       `json:"ThumbnailLength"`
	ThumbnailOffset                   int       `json:"ThumbnailOffset"`
	Title                             string    `json:"Title"`
	TransferCharacteristics           int       `json:"TransferCharacteristics"`
	UserComment                       string    `json:"UserComment"`
	VP8Version                        int       `json:"VP8Version"`
	VerticalScale                     int       `json:"VerticalScale"`
	VideoFullRangeFlag                int       `json:"VideoFullRangeFlag"`
	Warning                           string    `json:"Warning"`
	WhiteBalance                      int       `json:"WhiteBalance"`
	WhitePointX                       float64   `json:"WhitePointX"`
	WhitePointY                       float64   `json:"WhitePointY"`
	XMPToolkit                        string    `json:"XMPToolkit"`
	XResolution                       int       `json:"XResolution"`
	YCbCrSubSampling                  string    `json:"YCbCrSubSampling"`
	YResolution                       int       `json:"YResolution"`
}

func (Png) MediaType() string { return MediaTypeImage }

func (p Png) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:      ftoa(p.ExifToolVersion),
		SourceFile:           p.SourceFile,
		Directory:            p.Directory,
		FileName:             p.FileName,
		FileSize:             itoa(p.FileSize),
		FilePermissions:      itoa(p.FilePermissions),
		FileType:             p.FileType,
		FileTypeExtension:    p.FileTypeExtension,
		MIMEType:             p.MIMEType,
		FileModifyDate:       p.FileModifyDate,
		FileAccessDate:       p.FileAccessDate,
		FileInodeChangeDate:  p.FileInodeChangeDate,
		ImageWidth:           itoa(p.ImageWidth),
		ImageHeight:          itoa(p.ImageHeight),
		ImageSize:            p.ImageSize,
		Megapixels:           ftoa(p.Megapixels),
		Orientation:          itoa(p.Orientation),
		Make:                 p.Make,
		Model:                p.Model,
		LensModel:            p.LensModel,
		Software:             p.Software,
		CreateDate:           p.CreateDate,
		ModifyDate:           p.ModifyDate,
		DateTimeOriginal:     p.DateTimeOriginal,
		ISO:                  itoa(p.Iso),
		Aperture:             ftoa(p.Aperture),
		FNumber:              ftoa(p.FNumber),
		FocalLength:          ftoa(p.FocalLength),
		ExposureTime:         ftoa(p.ExposureTime),
		ShutterSpeed:         ftoa(p.ShutterSpeed),
		ExposureMode:         itoa(p.ExposureMode),
		ExposureProgram:      itoa(p.ExposureProgram),
		ExposureCompensation: itoa(p.ExposureCompensation),
		Flash:                itoa(p.Flash),
		MeteringMode:         itoa(p.MeteringMode),
		WhiteBalance:         itoa(p.WhiteBalance),
		GPSLatitude:          ftoa(p.GPSLatitude),
		GPSLongitude:         ftoa(p.GPSLongitude),
	}
}
