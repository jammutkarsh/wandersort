package classifier

type Cr2 struct {
	AEBBracketValue            int     `json:"AEBBracketValue"`
	AESetting                  int     `json:"AESetting"`
	AFAreaHeights              string  `json:"AFAreaHeights"`
	AFAreaMode                 int     `json:"AFAreaMode"`
	AFAreaWidths               string  `json:"AFAreaWidths"`
	AFAreaXPositions           string  `json:"AFAreaXPositions"`
	AFAreaYPositions           string  `json:"AFAreaYPositions"`
	AFAssistBeam               int     `json:"AFAssistBeam"`
	AFImageHeight              int     `json:"AFImageHeight"`
	AFImageWidth               int     `json:"AFImageWidth"`
	AFMicroAdjMode             int     `json:"AFMicroAdjMode"`
	AFMicroAdjValue            int     `json:"AFMicroAdjValue"`
	AFPointsInFocus            string  `json:"AFPointsInFocus"`
	AFPointsSelected           string  `json:"AFPointsSelected"`
	AmbienceSelection          int     `json:"AmbienceSelection"`
	Aperture                   float64 `json:"Aperture"`
	ApertureValue              float64 `json:"ApertureValue"`
	Artist                     string  `json:"Artist"`
	AspectRatio                int     `json:"AspectRatio"`
	AutoExposureBracketing     int     `json:"AutoExposureBracketing"`
	AutoISO                    int     `json:"AutoISO"`
	AutoLightingOptimizer      int     `json:"AutoLightingOptimizer"`
	AverageBlackLevel          string  `json:"AverageBlackLevel"`
	BaseISO                    int     `json:"BaseISO"`
	BitsPerSample              int     `json:"BitsPerSample"`
	BlackMaskBottomBorder      int     `json:"BlackMaskBottomBorder"`
	BlackMaskLeftBorder        int     `json:"BlackMaskLeftBorder"`
	BlackMaskRightBorder       int     `json:"BlackMaskRightBorder"`
	BlackMaskTopBorder         int     `json:"BlackMaskTopBorder"`
	BlueBalance                float64 `json:"BlueBalance"`
	BracketMode                int     `json:"BracketMode"`
	BracketShotNumber          int     `json:"BracketShotNumber"`
	BracketValue               int     `json:"BracketValue"`
	BulbDuration               int     `json:"BulbDuration"`
	CR2CFAPattern              string  `json:"CR2CFAPattern"`
	CameraISO                  string  `json:"CameraISO"`
	CameraOrientation          int     `json:"CameraOrientation"`
	CameraTemperature          int     `json:"CameraTemperature"`
	CameraType                 int     `json:"CameraType"`
	CanonExposureMode          int     `json:"CanonExposureMode"`
	CanonFirmwareVersion       string  `json:"CanonFirmwareVersion"`
	CanonFlashMode             int     `json:"CanonFlashMode"`
	CanonImageHeight           int     `json:"CanonImageHeight"`
	CanonImageSize             int     `json:"CanonImageSize"`
	CanonImageType             string  `json:"CanonImageType"`
	CanonImageWidth            int     `json:"CanonImageWidth"`
	CanonModelID               int64   `json:"CanonModelID"`
	ChromaticAberrationCorr    int     `json:"ChromaticAberrationCorr"`
	ChromaticAberrationSetting int     `json:"ChromaticAberrationSetting"`
	CircleOfConfusion          float64 `json:"CircleOfConfusion"`
	ColorComponents            int     `json:"ColorComponents"`
	ColorDataVersion           int     `json:"ColorDataVersion"`
	ColorSpace                 int     `json:"ColorSpace"`
	ColorTempAsShot            int     `json:"ColorTempAsShot"`
	ColorTempAuto              int     `json:"ColorTempAuto"`
	ColorTempCloudy            int     `json:"ColorTempCloudy"`
	ColorTempDaylight          int     `json:"ColorTempDaylight"`
	ColorTempFlash             int     `json:"ColorTempFlash"`
	ColorTempFluorescent       int     `json:"ColorTempFluorescent"`
	ColorTempKelvin            int     `json:"ColorTempKelvin"`
	ColorTempMeasured          int     `json:"ColorTempMeasured"`
	ColorTempShade             int     `json:"ColorTempShade"`
	ColorTempTungsten          int     `json:"ColorTempTungsten"`
	ColorTemperature           int     `json:"ColorTemperature"`
	ColorTone                  int     `json:"ColorTone"`
	ColorToneAuto              int     `json:"ColorToneAuto"`
	ColorToneFaithful          int     `json:"ColorToneFaithful"`
	ColorToneLandscape         int     `json:"ColorToneLandscape"`
	ColorToneNeutral           int     `json:"ColorToneNeutral"`
	ColorTonePortrait          int     `json:"ColorTonePortrait"`
	ColorToneStandard          int     `json:"ColorToneStandard"`
	ColorToneUserDef1          int     `json:"ColorToneUserDef1"`
	ColorToneUserDef2          int     `json:"ColorToneUserDef2"`
	ColorToneUserDef3          int     `json:"ColorToneUserDef3"`
	ComponentsConfiguration    string  `json:"ComponentsConfiguration"`
	Compression                int     `json:"Compression"`
	ConditionalFEC             int     `json:"ConditionalFEC"`
	ContinuousDrive            int     `json:"ContinuousDrive"`
	Contrast                   int     `json:"Contrast"`
	ContrastAuto               int     `json:"ContrastAuto"`
	ContrastFaithful           int     `json:"ContrastFaithful"`
	ContrastLandscape          int     `json:"ContrastLandscape"`
	ContrastMonochrome         int     `json:"ContrastMonochrome"`
	ContrastNeutral            int     `json:"ContrastNeutral"`
	ContrastPortrait           int     `json:"ContrastPortrait"`
	ContrastStandard           int     `json:"ContrastStandard"`
	ContrastUserDef1           int     `json:"ContrastUserDef1"`
	ContrastUserDef2           int     `json:"ContrastUserDef2"`
	ContrastUserDef3           int     `json:"ContrastUserDef3"`
	ControlMode                int     `json:"ControlMode"`
	Copyright                  string  `json:"Copyright"`
	CreateDate                 string  `json:"CreateDate"`
	CropBottomMargin           int     `json:"CropBottomMargin"`
	CropLeftMargin             int     `json:"CropLeftMargin"`
	CropRightMargin            int     `json:"CropRightMargin"`
	CropTopMargin              int     `json:"CropTopMargin"`
	CroppedImageHeight         int     `json:"CroppedImageHeight"`
	CroppedImageLeft           int     `json:"CroppedImageLeft"`
	CroppedImageTop            int     `json:"CroppedImageTop"`
	CroppedImageWidth          int     `json:"CroppedImageWidth"`
	CurrentIPTCDigest          string  `json:"CurrentIPTCDigest"`
	CustomPictureStyleFileName string  `json:"CustomPictureStyleFileName"`
	CustomRendered             int     `json:"CustomRendered"`
	Dof                        string  `json:"DOF"`
	DateCreated                string  `json:"DateCreated"`
	DateTimeOriginal           string  `json:"DateTimeOriginal"`
	DaylightSavings            int     `json:"DaylightSavings"`
	DigitalGain                int     `json:"DigitalGain"`
	DigitalZoom                int     `json:"DigitalZoom"`
	Directory                  string  `json:"Directory"`
	DirectoryIndex             int     `json:"DirectoryIndex"`
	DistortionCorrection       int     `json:"DistortionCorrection"`
	DistortionCorrectionValue  int     `json:"DistortionCorrectionValue"`
	DriveMode                  int     `json:"DriveMode"`
	DustRemovalData            string  `json:"DustRemovalData"`
	EasyMode                   int     `json:"EasyMode"`
	EncodingProcess            int     `json:"EncodingProcess"`
	ExifByteOrder              string  `json:"ExifByteOrder"`
	ExifImageHeight            int     `json:"ExifImageHeight"`
	ExifImageWidth             int     `json:"ExifImageWidth"`
	ExifToolVersion            float64 `json:"ExifToolVersion"`
	ExifVersion                string  `json:"ExifVersion"`
	ExposureCompensation       int     `json:"ExposureCompensation"`
	ExposureLevelIncrements    int     `json:"ExposureLevelIncrements"`
	ExposureMode               int     `json:"ExposureMode"`
	ExposureProgram            int     `json:"ExposureProgram"`
	ExposureTime               float64 `json:"ExposureTime"`
	FNumber                    float64 `json:"FNumber"`
	Fov                        float64 `json:"FOV"`
	FileAccessDate             string  `json:"FileAccessDate"`
	FileIndex                  int     `json:"FileIndex"`
	FileInodeChangeDate        string  `json:"FileInodeChangeDate"`
	FileModifyDate             string  `json:"FileModifyDate"`
	FileName                   string  `json:"FileName"`
	FileNumber                 int     `json:"FileNumber"`
	FilePermissions            int     `json:"FilePermissions"`
	FileSize                   int     `json:"FileSize"`
	FileType                   string  `json:"FileType"`
	FileTypeExtension          string  `json:"FileTypeExtension"`
	FilterEffectAuto           int     `json:"FilterEffectAuto"`
	FilterEffectMonochrome     int     `json:"FilterEffectMonochrome"`
	FilterEffectUserDef1       int     `json:"FilterEffectUserDef1"`
	FilterEffectUserDef2       int     `json:"FilterEffectUserDef2"`
	FilterEffectUserDef3       int     `json:"FilterEffectUserDef3"`
	FirmwareVersion            string  `json:"FirmwareVersion"`
	Flash                      int     `json:"Flash"`
	FlashBatteryLevel          int     `json:"FlashBatteryLevel"`
	FlashBits                  int     `json:"FlashBits"`
	FlashExposureComp          int     `json:"FlashExposureComp"`
	FlashExposureLock          int     `json:"FlashExposureLock"`
	FlashGuideNumber           int     `json:"FlashGuideNumber"`
	FlashModel                 int     `json:"FlashModel"`
	FlashOutput                string  `json:"FlashOutput"`
	FlashType                  int     `json:"FlashType"`
	FlashpixVersion            string  `json:"FlashpixVersion"`
	FocalLength                int     `json:"FocalLength"`
	FocalLength35Efl           int     `json:"FocalLength35efl"`
	FocalPlaneResolutionUnit   int     `json:"FocalPlaneResolutionUnit"`
	FocalPlaneXResolution      float64 `json:"FocalPlaneXResolution"`
	FocalPlaneYResolution      float64 `json:"FocalPlaneYResolution"`
	FocalUnits                 int     `json:"FocalUnits"`
	FocusDistanceLower         float64 `json:"FocusDistanceLower"`
	FocusDistanceUpper         float64 `json:"FocusDistanceUpper"`
	FocusMode                  int     `json:"FocusMode"`
	FocusRange                 int     `json:"FocusRange"`
	GPSVersionID               string  `json:"GPSVersionID"`
	Hdr                        int     `json:"HDR"`
	HDREffect                  int     `json:"HDREffect"`
	HighISONoiseReduction      int     `json:"HighISONoiseReduction"`
	HighlightTonePriority      int     `json:"HighlightTonePriority"`
	HyperfocalDistance         float64 `json:"HyperfocalDistance"`
	IPTCDigest                 string  `json:"IPTCDigest"`
	Iso                        int     `json:"ISO"`
	ISOExpansion               int     `json:"ISOExpansion"`
	ImageHeight                int     `json:"ImageHeight"`
	ImageSize                  string  `json:"ImageSize"`
	ImageUniqueID              string  `json:"ImageUniqueID"`
	ImageWidth                 int     `json:"ImageWidth"`
	InternalSerialNumber       string  `json:"InternalSerialNumber"`
	InteropIndex               string  `json:"InteropIndex"`
	InteropVersion             string  `json:"InteropVersion"`
	JFIFVersion                string  `json:"JFIFVersion"`
	LCDDisplayAtPowerOn        int     `json:"LCDDisplayAtPowerOn"`
	Lens                       int     `json:"Lens"`
	Lens35Efl                  float64 `json:"Lens35efl"`
	LensID                     int     `json:"LensID"`
	LensInfo                   string  `json:"LensInfo"`
	LensModel                  string  `json:"LensModel"`
	LensSerialNumber           string  `json:"LensSerialNumber"`
	LensType                   int     `json:"LensType"`
	LightValue                 float64 `json:"LightValue"`
	LinearityUpperMargin       int     `json:"LinearityUpperMargin"`
	LiveViewShooting           int     `json:"LiveViewShooting"`
	LongExposureNoiseReduction int     `json:"LongExposureNoiseReduction"`
	MIMEType                   string  `json:"MIMEType"`
	MacroMode                  int     `json:"MacroMode"`
	Make                       string  `json:"Make"`
	ManualFlashOutput          int     `json:"ManualFlashOutput"`
	MaxAperture                float64 `json:"MaxAperture"`
	MaxFocalLength             int     `json:"MaxFocalLength"`
	MeasuredEV                 float64 `json:"MeasuredEV"`
	MeasuredEV2                float64 `json:"MeasuredEV2"`
	MeasuredRGGB               string  `json:"MeasuredRGGB"`
	Megapixels                 float64 `json:"Megapixels"`
	MeteringMode               int     `json:"MeteringMode"`
	MinAperture                float64 `json:"MinAperture"`
	MinFocalLength             int     `json:"MinFocalLength"`
	MirrorLockup               int     `json:"MirrorLockup"`
	Model                      string  `json:"Model"`
	ModifyDate                 string  `json:"ModifyDate"`
	NDFilter                   int     `json:"NDFilter"`
	NormalWhiteLevel           int     `json:"NormalWhiteLevel"`
	NumAFPoints                int     `json:"NumAFPoints"`
	OpticalZoomCode            int     `json:"OpticalZoomCode"`
	Orientation                int     `json:"Orientation"`
	OriginalImageHeight        int     `json:"OriginalImageHeight"`
	OriginalImageWidth         int     `json:"OriginalImageWidth"`
	OwnerName                  string  `json:"OwnerName"`
	PerChannelBlackLevel       string  `json:"PerChannelBlackLevel"`
	PeripheralIlluminationCorr int     `json:"PeripheralIlluminationCorr"`
	PeripheralLighting         int     `json:"PeripheralLighting"`
	PeripheralLightingSetting  int     `json:"PeripheralLightingSetting"`
	PeripheralLightingValue    int     `json:"PeripheralLightingValue"`
	PhotometricInterpretation  int     `json:"PhotometricInterpretation"`
	PictureStyle               int     `json:"PictureStyle"`
	PictureStylePC             string  `json:"PictureStylePC"`
	PictureStyleUserDef        string  `json:"PictureStyleUserDef"`
	PlanarConfiguration        int     `json:"PlanarConfiguration"`
	PreviewImage               string  `json:"PreviewImage"`
	PreviewImageLength         int     `json:"PreviewImageLength"`
	PreviewImageStart          int     `json:"PreviewImageStart"`
	Quality                    int     `json:"Quality"`
	Rating                     int     `json:"Rating"`
	RawImageSegmentation       string  `json:"RawImageSegmentation"`
	RawJpgSize                 int     `json:"RawJpgSize"`
	RawMeasuredRGGB            string  `json:"RawMeasuredRGGB"`
	RecommendedExposureIndex   int     `json:"RecommendedExposureIndex"`
	RecordMode                 int     `json:"RecordMode"`
	RedBalance                 float64 `json:"RedBalance"`
	RedEyeReduction            int     `json:"RedEyeReduction"`
	ResolutionUnit             int     `json:"ResolutionUnit"`
	RowsPerStrip               int     `json:"RowsPerStrip"`
	SRAWQuality                int     `json:"SRAWQuality"`
	SRawType                   int     `json:"SRawType"`
	SamplesPerPixel            int     `json:"SamplesPerPixel"`
	Saturation                 int     `json:"Saturation"`
	SaturationAuto             int     `json:"SaturationAuto"`
	SaturationFaithful         int     `json:"SaturationFaithful"`
	SaturationLandscape        int     `json:"SaturationLandscape"`
	SaturationNeutral          int     `json:"SaturationNeutral"`
	SaturationPortrait         int     `json:"SaturationPortrait"`
	SaturationStandard         int     `json:"SaturationStandard"`
	SaturationUserDef1         int     `json:"SaturationUserDef1"`
	SaturationUserDef2         int     `json:"SaturationUserDef2"`
	SaturationUserDef3         int     `json:"SaturationUserDef3"`
	ScaleFactor35Efl           float64 `json:"ScaleFactor35efl"`
	SceneCaptureType           int     `json:"SceneCaptureType"`
	SelfTimer                  int     `json:"SelfTimer"`
	SensitivityType            int     `json:"SensitivityType"`
	SensorBlueLevel            int     `json:"SensorBlueLevel"`
	SensorBottomBorder         int     `json:"SensorBottomBorder"`
	SensorHeight               int     `json:"SensorHeight"`
	SensorLeftBorder           int     `json:"SensorLeftBorder"`
	SensorRedLevel             int     `json:"SensorRedLevel"`
	SensorRightBorder          int     `json:"SensorRightBorder"`
	SensorTopBorder            int     `json:"SensorTopBorder"`
	SensorWidth                int     `json:"SensorWidth"`
	SequenceNumber             int     `json:"SequenceNumber"`
	SerialNumber               string  `json:"SerialNumber"`
	SetButtonWhenShooting      int     `json:"SetButtonWhenShooting"`
	Sharpness                  int     `json:"Sharpness"`
	SharpnessAuto              int     `json:"SharpnessAuto"`
	SharpnessFaithful          int     `json:"SharpnessFaithful"`
	SharpnessFrequency         int     `json:"SharpnessFrequency"`
	SharpnessLandscape         int     `json:"SharpnessLandscape"`
	SharpnessMonochrome        int     `json:"SharpnessMonochrome"`
	SharpnessNeutral           int     `json:"SharpnessNeutral"`
	SharpnessPortrait          int     `json:"SharpnessPortrait"`
	SharpnessStandard          int     `json:"SharpnessStandard"`
	SharpnessUserDef1          int     `json:"SharpnessUserDef1"`
	SharpnessUserDef2          int     `json:"SharpnessUserDef2"`
	SharpnessUserDef3          int     `json:"SharpnessUserDef3"`
	ShootingMode               int     `json:"ShootingMode"`
	ShutterButtonAFOnButton    int     `json:"ShutterButtonAFOnButton"`
	ShutterCurtainHack         int     `json:"ShutterCurtainHack"`
	ShutterMode                int     `json:"ShutterMode"`
	ShutterSpeed               float64 `json:"ShutterSpeed"`
	ShutterSpeedValue          string  `json:"ShutterSpeedValue"`
	SlowShutter                int     `json:"SlowShutter"`
	Software                   string  `json:"Software"`
	SourceFile                 string  `json:"SourceFile"`
	SpecularWhiteLevel         int     `json:"SpecularWhiteLevel"`
	StripByteCounts            int     `json:"StripByteCounts"`
	StripOffsets               int     `json:"StripOffsets"`
	SubSecCreateDate           string  `json:"SubSecCreateDate"`
	SubSecDateTimeOriginal     string  `json:"SubSecDateTimeOriginal"`
	SubSecModifyDate           string  `json:"SubSecModifyDate"`
	SubSecTime                 int     `json:"SubSecTime"`
	SubSecTimeDigitized        int     `json:"SubSecTimeDigitized"`
	SubSecTimeOriginal         int     `json:"SubSecTimeOriginal"`
	TargetAperture             float64 `json:"TargetAperture"`
	TargetExposureTime         string  `json:"TargetExposureTime"`
	ThumbnailImage             string  `json:"ThumbnailImage"`
	ThumbnailImageValidArea    string  `json:"ThumbnailImageValidArea"`
	ThumbnailLength            int     `json:"ThumbnailLength"`
	ThumbnailOffset            int     `json:"ThumbnailOffset"`
	TimeZone                   int     `json:"TimeZone"`
	TimeZoneCity               int     `json:"TimeZoneCity"`
	ToneCurve                  int     `json:"ToneCurve"`
	ToningEffectAuto           int     `json:"ToningEffectAuto"`
	ToningEffectMonochrome     int     `json:"ToningEffectMonochrome"`
	ToningEffectUserDef1       int     `json:"ToningEffectUserDef1"`
	ToningEffectUserDef2       int     `json:"ToningEffectUserDef2"`
	ToningEffectUserDef3       int     `json:"ToningEffectUserDef3"`
	UserComment                string  `json:"UserComment"`
	UserDef1PictureStyle       int     `json:"UserDef1PictureStyle"`
	UserDef2PictureStyle       int     `json:"UserDef2PictureStyle"`
	UserDef3PictureStyle       int     `json:"UserDef3PictureStyle"`
	VRDOffset                  int     `json:"VRDOffset"`
	ValidAFPoints              int     `json:"ValidAFPoints"`
	VignettingCorrVersion      int     `json:"VignettingCorrVersion"`
	WBBracketMode              int     `json:"WBBracketMode"`
	WBBracketValueAB           int     `json:"WBBracketValueAB"`
	WBBracketValueGM           int     `json:"WBBracketValueGM"`
	WBShiftAB                  int     `json:"WBShiftAB"`
	WBShiftGM                  int     `json:"WBShiftGM"`
	WBRGGBLevels               string  `json:"WB_RGGBLevels"`
	WBRGGBLevelsAsShot         string  `json:"WB_RGGBLevelsAsShot"`
	WBRGGBLevelsAuto           string  `json:"WB_RGGBLevelsAuto"`
	WBRGGBLevelsCloudy         string  `json:"WB_RGGBLevelsCloudy"`
	WBRGGBLevelsDaylight       string  `json:"WB_RGGBLevelsDaylight"`
	WBRGGBLevelsFlash          string  `json:"WB_RGGBLevelsFlash"`
	WBRGGBLevelsFluorescent    string  `json:"WB_RGGBLevelsFluorescent"`
	WBRGGBLevelsKelvin         string  `json:"WB_RGGBLevelsKelvin"`
	WBRGGBLevelsMeasured       string  `json:"WB_RGGBLevelsMeasured"`
	WBRGGBLevelsShade          string  `json:"WB_RGGBLevelsShade"`
	WBRGGBLevelsTungsten       string  `json:"WB_RGGBLevelsTungsten"`
	WhiteBalance               int     `json:"WhiteBalance"`
	WhiteBalanceBlue           int     `json:"WhiteBalanceBlue"`
	WhiteBalanceRed            int     `json:"WhiteBalanceRed"`
	XMPToolkit                 string  `json:"XMPToolkit"`
	XResolution                int     `json:"XResolution"`
	YCbCrSubSampling           string  `json:"YCbCrSubSampling"`
	YResolution                int     `json:"YResolution"`
	ZoomSourceWidth            int     `json:"ZoomSourceWidth"`
	ZoomTargetWidth            int     `json:"ZoomTargetWidth"`
}

func (Cr2) MediaType() string { return MediaTypeRaw }

func (c Cr2) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:      ftoa(c.ExifToolVersion),
		SourceFile:           c.SourceFile,
		Directory:            c.Directory,
		FileName:             c.FileName,
		FileSize:             itoa(c.FileSize),
		FilePermissions:      itoa(c.FilePermissions),
		FileType:             c.FileType,
		FileTypeExtension:    c.FileTypeExtension,
		MIMEType:             c.MIMEType,
		FileModifyDate:       c.FileModifyDate,
		FileAccessDate:       c.FileAccessDate,
		FileInodeChangeDate:  c.FileInodeChangeDate,
		ImageWidth:           itoa(c.ImageWidth),
		ImageHeight:          itoa(c.ImageHeight),
		ImageSize:            c.ImageSize,
		Megapixels:           ftoa(c.Megapixels),
		Orientation:          itoa(c.Orientation),
		Make:                 c.Make,
		Model:                c.Model,
		LensModel:            c.LensModel,
		Software:             c.Software,
		CreateDate:           c.CreateDate,
		ModifyDate:           c.ModifyDate,
		DateTimeOriginal:     c.DateTimeOriginal,
		ISO:                  itoa(c.Iso),
		Aperture:             ftoa(c.Aperture),
		FNumber:              ftoa(c.FNumber),
		FocalLength:          itoa(c.FocalLength),
		ExposureTime:         ftoa(c.ExposureTime),
		ShutterSpeed:         ftoa(c.ShutterSpeed),
		ExposureMode:         itoa(c.ExposureMode),
		ExposureProgram:      itoa(c.ExposureProgram),
		ExposureCompensation: itoa(c.ExposureCompensation),
		Flash:                itoa(c.Flash),
		MeteringMode:         itoa(c.MeteringMode),
		WhiteBalance:         itoa(c.WhiteBalance),
	}
}
