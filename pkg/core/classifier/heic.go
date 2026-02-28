package classifier

type Heic struct {
	AEAverage                                          int       `json:"AEAverage"`
	AEStable                                           int       `json:"AEStable"`
	AETarget                                           int       `json:"AETarget"`
	AFConfidence                                       int       `json:"AFConfidence"`
	AFMeasuredDepth                                    int       `json:"AFMeasuredDepth"`
	AFPerformance                                      string    `json:"AFPerformance"`
	AFStable                                           int       `json:"AFStable"`
	AToB0                                              string    `json:"AToB0"`
	AToB1                                              string    `json:"AToB1"`
	AToB2                                              string    `json:"AToB2"`
	AccelerationVector                                 string    `json:"AccelerationVector"`
	Accuracy                                           string    `json:"Accuracy"`
	Aperture                                           float64   `json:"Aperture"`
	ApertureValue                                      float64   `json:"ApertureValue"`
	AuxiliaryImageSubType                              string    `json:"AuxiliaryImageSubType"`
	AuxiliaryImageType                                 string    `json:"AuxiliaryImageType"`
	AverageFrameRate                                   int       `json:"AverageFrameRate"`
	BToA0                                              string    `json:"BToA0"`
	BaseRenditionIsHDR                                 bool      `json:"BaseRenditionIsHDR"`
	BitDepthChroma                                     int       `json:"BitDepthChroma"`
	BitDepthLuma                                       int       `json:"BitDepthLuma"`
	BitsPerSample                                      int       `json:"BitsPerSample"`
	BlueMatrixColumn                                   string    `json:"BlueMatrixColumn"`
	BlueTRC                                            string    `json:"BlueTRC"`
	BrightnessValue                                    float64   `json:"BrightnessValue"`
	BurstUUID                                          string    `json:"BurstUUID"`
	CMMFlags                                           int       `json:"CMMFlags"`
	CameraType                                         int       `json:"CameraType"`
	ChromaFormat                                       int       `json:"ChromaFormat"`
	ChromaticAdaptation                                string    `json:"ChromaticAdaptation"`
	CircleOfConfusion                                  string    `json:"CircleOfConfusion"`
	CleanAperture                                      string    `json:"CleanAperture"`
	ColorComponents                                    int       `json:"ColorComponents"`
	ColorPrimaries                                     int       `json:"ColorPrimaries"`
	ColorProfiles                                      string    `json:"ColorProfiles"`
	ColorSpace                                         int       `json:"ColorSpace"`
	ColorSpaceData                                     string    `json:"ColorSpaceData"`
	ColorTemperature                                   int       `json:"ColorTemperature"`
	CompatibleBrands                                   []string  `json:"CompatibleBrands"`
	ComponentsConfiguration                            string    `json:"ComponentsConfiguration"`
	CompositeImage                                     int       `json:"CompositeImage"`
	CompositeImageCount                                string    `json:"CompositeImageCount"`
	CompositeImageExposureTimes                        string    `json:"CompositeImageExposureTimes"`
	Compression                                        int       `json:"Compression"`
	ConnectionSpaceIlluminant                          string    `json:"ConnectionSpaceIlluminant"`
	ConstantFrameRate                                  int       `json:"ConstantFrameRate"`
	ConstraintIndicatorFlags                           string    `json:"ConstraintIndicatorFlags"`
	ContentIdentifier                                  string    `json:"ContentIdentifier"`
	CreateDate                                         string    `json:"CreateDate"`
	CreatorTool                                        float64   `json:"CreatorTool"`
	CurrentIPTCDigest                                  string    `json:"CurrentIPTCDigest"`
	CustomRendered                                     int       `json:"CustomRendered"`
	DateCreated                                        string    `json:"DateCreated"`
	DateTimeOriginal                                   string    `json:"DateTimeOriginal"`
	DependentImage1EntryNumber                         int       `json:"DependentImage1EntryNumber"`
	DependentImage2EntryNumber                         int       `json:"DependentImage2EntryNumber"`
	DepthDataVersion                                   int       `json:"DepthDataVersion"`
	DeviceAttributes                                   string    `json:"DeviceAttributes"`
	DeviceManufacturer                                 string    `json:"DeviceManufacturer"`
	DeviceModel                                        string    `json:"DeviceModel"`
	DigitalZoomRatio                                   float64   `json:"DigitalZoomRatio"`
	Directory                                          string    `json:"Directory"`
	DirectoryItemLength                                []int     `json:"DirectoryItemLength"`
	DirectoryItemMime                                  []string  `json:"DirectoryItemMime"`
	DirectoryItemPadding                               []int     `json:"DirectoryItemPadding"`
	DirectoryItemSemantic                              []string  `json:"DirectoryItemSemantic"`
	EffectStrength                                     float64   `json:"EffectStrength"`
	EmbeddedVideoOffsetSize                            string    `json:"EmbeddedVideoOffsetSize"`
	EmbeddedVideoType                                  string    `json:"EmbeddedVideoType"`
	EncodingProcess                                    int       `json:"EncodingProcess"`
	ExifByteOrder                                      string    `json:"ExifByteOrder"`
	ExifImageHeight                                    int       `json:"ExifImageHeight"`
	ExifImageWidth                                     int       `json:"ExifImageWidth"`
	ExifToolVersion                                    float64   `json:"ExifToolVersion"`
	ExifVersion                                        string    `json:"ExifVersion"`
	ExposureCompensation                               int       `json:"ExposureCompensation"`
	ExposureMode                                       int       `json:"ExposureMode"`
	ExposureProgram                                    int       `json:"ExposureProgram"`
	ExposureTime                                       float64   `json:"ExposureTime"`
	ExtrinsicMatrix                                    []int     `json:"ExtrinsicMatrix"`
	FNumber                                            float64   `json:"FNumber"`
	Fov                                                float64   `json:"FOV"`
	FileAccessDate                                     string    `json:"FileAccessDate"`
	FileInodeChangeDate                                string    `json:"FileInodeChangeDate"`
	FileModifyDate                                     string    `json:"FileModifyDate"`
	FileName                                           string    `json:"FileName"`
	FilePermissions                                    int       `json:"FilePermissions"`
	FileSize                                           int       `json:"FileSize"`
	FileType                                           string    `json:"FileType"`
	FileTypeExtension                                  string    `json:"FileTypeExtension"`
	Filtered                                           bool      `json:"Filtered"`
	Flash                                              int       `json:"Flash"`
	FlashpixVersion                                    string    `json:"FlashpixVersion"`
	FloatMaxValue                                      float64   `json:"FloatMaxValue"`
	FloatMinValue                                      int       `json:"FloatMinValue"`
	FocalLength                                        float64   `json:"FocalLength"`
	FocalLength35Efl                                   int       `json:"FocalLength35efl"`
	FocalLengthIn35MmFormat                            int       `json:"FocalLengthIn35mmFormat"`
	FocusDistanceRange                                 string    `json:"FocusDistanceRange"`
	FocusPosition                                      int       `json:"FocusPosition"`
	GPSAltitude                                        float64   `json:"GPSAltitude"`
	GPSAltitudeRef                                     int       `json:"GPSAltitudeRef"`
	GPSDateStamp                                       string    `json:"GPSDateStamp"`
	GPSDateTime                                        string    `json:"GPSDateTime"`
	GPSDestBearing                                     float64   `json:"GPSDestBearing"`
	GPSDestBearingRef                                  string    `json:"GPSDestBearingRef"`
	GPSHPositioningError                               float64   `json:"GPSHPositioningError"`
	GPSImgDirection                                    float64   `json:"GPSImgDirection"`
	GPSImgDirectionRef                                 string    `json:"GPSImgDirectionRef"`
	GPSLatitude                                        float64   `json:"GPSLatitude"`
	GPSLatitudeRef                                     string    `json:"GPSLatitudeRef"`
	GPSLongitude                                       float64   `json:"GPSLongitude"`
	GPSLongitudeRef                                    string    `json:"GPSLongitudeRef"`
	GPSPosition                                        string    `json:"GPSPosition"`
	GPSSpeed                                           float64   `json:"GPSSpeed"`
	GPSSpeedRef                                        string    `json:"GPSSpeedRef"`
	GPSTimeStamp                                       string    `json:"GPSTimeStamp"`
	GPSVersionID                                       string    `json:"GPSVersionID"`
	GainMapMax                                         float64   `json:"GainMapMax"`
	GainMapMin                                         int       `json:"GainMapMin"`
	Gamma                                              int       `json:"Gamma"`
	GenProfileCompatibilityFlags                       int       `json:"GenProfileCompatibilityFlags"`
	GeneralLevelIDC                                    int       `json:"GeneralLevelIDC"`
	GeneralProfileIDC                                  int       `json:"GeneralProfileIDC"`
	GeneralProfileSpace                                int       `json:"GeneralProfileSpace"`
	GeneralTierFlag                                    int       `json:"GeneralTierFlag"`
	GrayTRC                                            string    `json:"GrayTRC"`
	GreenMatrixColumn                                  string    `json:"GreenMatrixColumn"`
	GreenTRC                                           string    `json:"GreenTRC"`
	HDGainMapInfo                                      string    `json:"HDGainMapInfo"`
	HDRCapacityMax                                     float64   `json:"HDRCapacityMax"`
	HDRCapacityMin                                     int       `json:"HDRCapacityMin"`
	HDRGain                                            float64   `json:"HDRGain"`
	HDRGainMapHeadroom                                 float64   `json:"HDRGainMapHeadroom"`
	HDRGainMapVersion                                  int       `json:"HDRGainMapVersion"`
	HDRHeadroom                                        float64   `json:"HDRHeadroom"`
	HEVCConfigurationVersion                           int       `json:"HEVCConfigurationVersion"`
	HandlerType                                        string    `json:"HandlerType"`
	HostComputer                                       string    `json:"HostComputer"`
	HyperfocalDistance                                 float64   `json:"HyperfocalDistance"`
	IGain                                              float64   `json:"IGain"`
	IOriginalRangeMax                                  float64   `json:"IOriginalRangeMax"`
	IOriginalRangeMin                                  string    `json:"IOriginalRangeMin"`
	IPTCDigest                                         string    `json:"IPTCDigest"`
	Iso                                                int       `json:"ISO"`
	ImageCaptureType                                   int       `json:"ImageCaptureType"`
	ImageHeight                                        int       `json:"ImageHeight"`
	ImagePixelDepth                                    string    `json:"ImagePixelDepth"`
	ImageSize                                          string    `json:"ImageSize"`
	ImageSpatialExtent                                 string    `json:"ImageSpatialExtent"`
	ImageUniqueID                                      string    `json:"ImageUniqueID"`
	ImageWidth                                         int       `json:"ImageWidth"`
	IntMaxValue                                        int       `json:"IntMaxValue"`
	IntMinValue                                        int       `json:"IntMinValue"`
	IntrinsicMatrix                                    []float64 `json:"IntrinsicMatrix"`
	IntrinsicMatrixReferenceHeight                     int       `json:"IntrinsicMatrixReferenceHeight"`
	IntrinsicMatrixReferenceWidth                      int       `json:"IntrinsicMatrixReferenceWidth"`
	InverseLensDistortionCoefficients                  []any     `json:"InverseLensDistortionCoefficients"`
	JFIFVersion                                        string    `json:"JFIFVersion"`
	LensDistortionCenterOffsetX                        float64   `json:"LensDistortionCenterOffsetX"`
	LensDistortionCenterOffsetY                        float64   `json:"LensDistortionCenterOffsetY"`
	LensDistortionCoefficients                         []any     `json:"LensDistortionCoefficients"`
	LensID                                             string    `json:"LensID"`
	LensInfo                                           string    `json:"LensInfo"`
	LensMake                                           string    `json:"LensMake"`
	LensModel                                          string    `json:"LensModel"`
	LightValue                                         float64   `json:"LightValue"`
	LivePhotoVideoIndex                                int64     `json:"LivePhotoVideoIndex"`
	Luminance                                          string    `json:"Luminance"`
	LuminanceNoiseAmplitude                            float64   `json:"LuminanceNoiseAmplitude"`
	MCCData                                            int       `json:"MCCData"`
	MIMEType                                           string    `json:"MIMEType"`
	MPFVersion                                         string    `json:"MPFVersion"`
	MPImage2                                           string    `json:"MPImage2"`
	MPImageFlags                                       int       `json:"MPImageFlags"`
	MPImageFormat                                      int       `json:"MPImageFormat"`
	MPImageLength                                      int       `json:"MPImageLength"`
	MPImageStart                                       int       `json:"MPImageStart"`
	MPImageType                                        int       `json:"MPImageType"`
	MajorBrand                                         string    `json:"MajorBrand"`
	Make                                               string    `json:"Make"`
	MakerNoteVersion                                   int       `json:"MakerNoteVersion"`
	MatrixCoefficients                                 int       `json:"MatrixCoefficients"`
	MaxApertureValue                                   float64   `json:"MaxApertureValue"`
	MaxContentLightLevel                               int       `json:"MaxContentLightLevel"`
	MaxPicAverageLightLevel                            int       `json:"MaxPicAverageLightLevel"`
	MediaDataOffset                                    int       `json:"MediaDataOffset"`
	MediaDataSize                                      int       `json:"MediaDataSize"`
	MediaWhitePoint                                    string    `json:"MediaWhitePoint"`
	Megapixels                                         float64   `json:"Megapixels"`
	MetaImageSize                                      string    `json:"MetaImageSize"`
	MeteringMode                                       int       `json:"MeteringMode"`
	MinSpatialSegmentationIDC                          int       `json:"MinSpatialSegmentationIDC"`
	MinorVersion                                       string    `json:"MinorVersion"`
	Mirroring                                          int       `json:"Mirroring"`
	Model                                              string    `json:"Model"`
	ModifyDate                                         string    `json:"ModifyDate"`
	MotionPhoto                                        int       `json:"MotionPhoto"`
	MotionPhotoAutoPlayVideo                           string    `json:"MotionPhotoAutoPlayVideo"`
	MotionPhotoPresentationTimestampUs                 int       `json:"MotionPhotoPresentationTimestampUs"`
	MotionPhotoVersion                                 int       `json:"MotionPhotoVersion"`
	MotionPhotoVideo                                   string    `json:"MotionPhotoVideo"`
	NativeFormat                                       int       `json:"NativeFormat"`
	NumTemporalLayers                                  int       `json:"NumTemporalLayers"`
	NumberOfImages                                     int       `json:"NumberOfImages"`
	OISMode                                            int       `json:"OISMode"`
	OffsetHDR                                          int       `json:"OffsetHDR"`
	OffsetSDR                                          int       `json:"OffsetSDR"`
	OffsetTime                                         string    `json:"OffsetTime"`
	OffsetTimeDigitized                                string    `json:"OffsetTimeDigitized"`
	OffsetTimeOriginal                                 string    `json:"OffsetTimeOriginal"`
	Orientation                                        int       `json:"Orientation"`
	ParallelismType                                    int       `json:"ParallelismType"`
	PhotoIdentifier                                    string    `json:"PhotoIdentifier"`
	PhotosAppFeatureFlags                              int       `json:"PhotosAppFeatureFlags"`
	PixelSize                                          float64   `json:"PixelSize"`
	PortraitEffectsMatteVersion                        int       `json:"PortraitEffectsMatteVersion"`
	PortraitScore                                      int       `json:"PortraitScore"`
	PortraitScoreIsHigh                                bool      `json:"PortraitScoreIsHigh"`
	PrimaryItemReference                               int       `json:"PrimaryItemReference"`
	PrimaryPlatform                                    string    `json:"PrimaryPlatform"`
	ProfileCMMType                                     string    `json:"ProfileCMMType"`
	ProfileClass                                       string    `json:"ProfileClass"`
	ProfileConnectionSpace                             string    `json:"ProfileConnectionSpace"`
	ProfileCopyright                                   string    `json:"ProfileCopyright"`
	ProfileCreator                                     string    `json:"ProfileCreator"`
	ProfileDateTime                                    string    `json:"ProfileDateTime"`
	ProfileDescription                                 string    `json:"ProfileDescription"`
	ProfileDescriptionML                               string    `json:"ProfileDescriptionML"`
	ProfileDescriptionMLArEG                           string    `json:"ProfileDescriptionML-ar-EG"`
	ProfileDescriptionMLCaES                           string    `json:"ProfileDescriptionML-ca-ES"`
	ProfileDescriptionMLCsCZ                           string    `json:"ProfileDescriptionML-cs-CZ"`
	ProfileDescriptionMLDaDK                           string    `json:"ProfileDescriptionML-da-DK"`
	ProfileDescriptionMLDeDE                           string    `json:"ProfileDescriptionML-de-DE"`
	ProfileDescriptionMLElGR                           string    `json:"ProfileDescriptionML-el-GR"`
	ProfileDescriptionMLEsES                           string    `json:"ProfileDescriptionML-es-ES"`
	ProfileDescriptionMLFiFI                           string    `json:"ProfileDescriptionML-fi-FI"`
	ProfileDescriptionMLFrFU                           string    `json:"ProfileDescriptionML-fr-FU"`
	ProfileDescriptionMLHeIL                           string    `json:"ProfileDescriptionML-he-IL"`
	ProfileDescriptionMLHrHR                           string    `json:"ProfileDescriptionML-hr-HR"`
	ProfileDescriptionMLHuHU                           string    `json:"ProfileDescriptionML-hu-HU"`
	ProfileDescriptionMLItIT                           string    `json:"ProfileDescriptionML-it-IT"`
	ProfileDescriptionMLJaJP                           string    `json:"ProfileDescriptionML-ja-JP"`
	ProfileDescriptionMLKoKR                           string    `json:"ProfileDescriptionML-ko-KR"`
	ProfileDescriptionMLNbNO                           string    `json:"ProfileDescriptionML-nb-NO"`
	ProfileDescriptionMLNlNL                           string    `json:"ProfileDescriptionML-nl-NL"`
	ProfileDescriptionMLPlPL                           string    `json:"ProfileDescriptionML-pl-PL"`
	ProfileDescriptionMLPtBR                           string    `json:"ProfileDescriptionML-pt-BR"`
	ProfileDescriptionMLPtPO                           string    `json:"ProfileDescriptionML-pt-PO"`
	ProfileDescriptionMLRoRO                           string    `json:"ProfileDescriptionML-ro-RO"`
	ProfileDescriptionMLRuRU                           string    `json:"ProfileDescriptionML-ru-RU"`
	ProfileDescriptionMLSkSK                           string    `json:"ProfileDescriptionML-sk-SK"`
	ProfileDescriptionMLSvSE                           string    `json:"ProfileDescriptionML-sv-SE"`
	ProfileDescriptionMLThTH                           string    `json:"ProfileDescriptionML-th-TH"`
	ProfileDescriptionMLTrTR                           string    `json:"ProfileDescriptionML-tr-TR"`
	ProfileDescriptionMLUkUA                           string    `json:"ProfileDescriptionML-uk-UA"`
	ProfileDescriptionMLViVN                           string    `json:"ProfileDescriptionML-vi-VN"`
	ProfileDescriptionMLZhCN                           string    `json:"ProfileDescriptionML-zh-CN"`
	ProfileDescriptionMLZhTW                           string    `json:"ProfileDescriptionML-zh-TW"`
	ProfileFileSignature                               string    `json:"ProfileFileSignature"`
	ProfileID                                          string    `json:"ProfileID"`
	ProfileVersion                                     int       `json:"ProfileVersion"`
	Quality                                            string    `json:"Quality"`
	RedMatrixColumn                                    string    `json:"RedMatrixColumn"`
	RedTRC                                             string    `json:"RedTRC"`
	RegionAppliedToDimensionsH                         int       `json:"RegionAppliedToDimensionsH"`
	RegionAppliedToDimensionsUnit                      string    `json:"RegionAppliedToDimensionsUnit"`
	RegionAppliedToDimensionsW                         int       `json:"RegionAppliedToDimensionsW"`
	RegionAreaH                                        []string  `json:"RegionAreaH"`
	RegionAreaUnit                                     []string  `json:"RegionAreaUnit"`
	RegionAreaW                                        []string  `json:"RegionAreaW"`
	RegionAreaX                                        []string  `json:"RegionAreaX"`
	RegionAreaY                                        []string  `json:"RegionAreaY"`
	RegionExtensions                                   string    `json:"RegionExtensions"`
	RegionExtensionsAngleInfoRoll                      int       `json:"RegionExtensionsAngleInfoRoll"`
	RegionExtensionsAngleInfoYaw                       int       `json:"RegionExtensionsAngleInfoYaw"`
	RegionExtensionsConfidenceLevel                    int       `json:"RegionExtensionsConfidenceLevel"`
	RegionExtensionsFaceID                             int       `json:"RegionExtensionsFaceID"`
	RegionType                                         []string  `json:"RegionType"`
	RenderingIntent                                    int       `json:"RenderingIntent"`
	RenderingParameters                                string    `json:"RenderingParameters"`
	ResolutionUnit                                     int       `json:"ResolutionUnit"`
	Rotation                                           int       `json:"Rotation"`
	RunTimeEpoch                                       int       `json:"RunTimeEpoch"`
	RunTimeFlags                                       int       `json:"RunTimeFlags"`
	RunTimeScale                                       int       `json:"RunTimeScale"`
	RunTimeSincePowerUp                                float64   `json:"RunTimeSincePowerUp"`
	RunTimeValue                                       int64     `json:"RunTimeValue"`
	SamsungMotionPhotoVersion                          string    `json:"SamsungMotionPhotoVersion"`
	ScaleFactor35Efl                                   float64   `json:"ScaleFactor35efl"`
	SceneCaptureType                                   int       `json:"SceneCaptureType"`
	SceneType                                          int       `json:"SceneType"`
	SemanticSegmentationMatteVersion                   int       `json:"SemanticSegmentationMatteVersion"`
	SemanticStyle                                      string    `json:"SemanticStyle"`
	SemanticStyleRenderingVer                          bool      `json:"SemanticStyleRenderingVer"`
	SensingMethod                                      int       `json:"SensingMethod"`
	ShutterSpeed                                       float64   `json:"ShutterSpeed"`
	ShutterSpeedValue                                  float64   `json:"ShutterSpeedValue"`
	SignalToNoiseRatio                                 float64   `json:"SignalToNoiseRatio"`
	SimulatedAperture                                  float64   `json:"SimulatedAperture"`
	Software                                           float64   `json:"Software"`
	SourceFile                                         string    `json:"SourceFile"`
	StoredFormat                                       int       `json:"StoredFormat"`
	SubSecCreateDate                                   string    `json:"SubSecCreateDate"`
	SubSecDateTimeOriginal                             string    `json:"SubSecDateTimeOriginal"`
	SubSecModifyDate                                   string    `json:"SubSecModifyDate"`
	SubSecTime                                         int       `json:"SubSecTime"`
	SubSecTimeDigitized                                int       `json:"SubSecTimeDigitized"`
	SubSecTimeOriginal                                 int       `json:"SubSecTimeOriginal"`
	SubjectArea                                        string    `json:"SubjectArea"`
	Tag0                                               int       `json:"Tag0"`
	Tag1                                               string    `json:"Tag1"`
	Tag2                                               bool      `json:"Tag2"`
	Tag3                                               string    `json:"Tag3"`
	Tag4                                               float64   `json:"Tag4"`
	Tag5                                               int       `json:"Tag5"`
	Tag6LinearGTCImageBlackPoint                       int       `json:"Tag6LinearGTCImageBlackPoint"`
	Tag6LinearGTCImageHighKey                          int       `json:"Tag6LinearGTCImageHighKey"`
	Tag6LinearGTCImageP02                              int       `json:"Tag6LinearGTCImageP02"`
	Tag6LinearGTCImageP10                              int       `json:"Tag6LinearGTCImageP10"`
	Tag6LinearGTCImageP25                              int       `json:"Tag6LinearGTCImageP25"`
	Tag6LinearGTCImageP50                              int       `json:"Tag6LinearGTCImageP50"`
	Tag6LinearGTCImageP75                              int       `json:"Tag6LinearGTCImageP75"`
	Tag6LinearGTCImageP98                              int       `json:"Tag6LinearGTCImageP98"`
	Tag6LinearGTCImageWhitePoint                       int       `json:"Tag6LinearGTCImageWhitePoint"`
	Tag6LinearImageBlackPoint                          int       `json:"Tag6LinearImageBlackPoint"`
	Tag6LinearImageHighKey                             float64   `json:"Tag6LinearImageHighKey"`
	Tag6LinearImageP02                                 float64   `json:"Tag6LinearImageP02"`
	Tag6LinearImageP10                                 float64   `json:"Tag6LinearImageP10"`
	Tag6LinearImageP25                                 float64   `json:"Tag6LinearImageP25"`
	Tag6LinearImageP50                                 float64   `json:"Tag6LinearImageP50"`
	Tag6LinearImageP75                                 float64   `json:"Tag6LinearImageP75"`
	Tag6LinearImageP98                                 float64   `json:"Tag6LinearImageP98"`
	Tag6LinearImagePersonSegmentBasedBlackPoint        int       `json:"Tag6LinearImagePersonSegmentBasedBlackPoint"`
	Tag6LinearImagePersonSegmentBasedHighKey           float64   `json:"Tag6LinearImagePersonSegmentBasedHighKey"`
	Tag6LinearImagePersonSegmentBasedP02               float64   `json:"Tag6LinearImagePersonSegmentBasedP02"`
	Tag6LinearImagePersonSegmentBasedP10               float64   `json:"Tag6LinearImagePersonSegmentBasedP10"`
	Tag6LinearImagePersonSegmentBasedP25               float64   `json:"Tag6LinearImagePersonSegmentBasedP25"`
	Tag6LinearImagePersonSegmentBasedP50               float64   `json:"Tag6LinearImagePersonSegmentBasedP50"`
	Tag6LinearImagePersonSegmentBasedP75               float64   `json:"Tag6LinearImagePersonSegmentBasedP75"`
	Tag6LinearImagePersonSegmentBasedP98               float64   `json:"Tag6LinearImagePersonSegmentBasedP98"`
	Tag6LinearImagePersonSegmentBasedWhitePoint        float64   `json:"Tag6LinearImagePersonSegmentBasedWhitePoint"`
	Tag6LinearImageSkinBasedBlackPoint                 int       `json:"Tag6LinearImageSkinBasedBlackPoint"`
	Tag6LinearImageSkinBasedHighKey                    float64   `json:"Tag6LinearImageSkinBasedHighKey"`
	Tag6LinearImageSkinBasedP02                        float64   `json:"Tag6LinearImageSkinBasedP02"`
	Tag6LinearImageSkinBasedP10                        float64   `json:"Tag6LinearImageSkinBasedP10"`
	Tag6LinearImageSkinBasedP25                        float64   `json:"Tag6LinearImageSkinBasedP25"`
	Tag6LinearImageSkinBasedP50                        float64   `json:"Tag6LinearImageSkinBasedP50"`
	Tag6LinearImageSkinBasedP75                        float64   `json:"Tag6LinearImageSkinBasedP75"`
	Tag6LinearImageSkinBasedP98                        float64   `json:"Tag6LinearImageSkinBasedP98"`
	Tag6LinearImageSkinBasedWhitePoint                 float64   `json:"Tag6LinearImageSkinBasedWhitePoint"`
	Tag6LinearImageWhitePoint                          float64   `json:"Tag6LinearImageWhitePoint"`
	Tag6PeopleRatio                                    int       `json:"Tag6PeopleRatio"`
	Tag6ToneMappedImageBlackPoint                      int       `json:"Tag6ToneMappedImageBlackPoint"`
	Tag6ToneMappedImageBlueChannelSkinBasedBlackPoint  int       `json:"Tag6ToneMappedImageBlueChannelSkinBasedBlackPoint"`
	Tag6ToneMappedImageBlueChannelSkinBasedHighKey     float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedHighKey"`
	Tag6ToneMappedImageBlueChannelSkinBasedP02         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP02"`
	Tag6ToneMappedImageBlueChannelSkinBasedP10         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP10"`
	Tag6ToneMappedImageBlueChannelSkinBasedP25         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP25"`
	Tag6ToneMappedImageBlueChannelSkinBasedP50         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP50"`
	Tag6ToneMappedImageBlueChannelSkinBasedP75         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP75"`
	Tag6ToneMappedImageBlueChannelSkinBasedP98         float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedP98"`
	Tag6ToneMappedImageBlueChannelSkinBasedWhitePoint  float64   `json:"Tag6ToneMappedImageBlueChannelSkinBasedWhitePoint"`
	Tag6ToneMappedImageGreenChannelSkinBasedBlackPoint int       `json:"Tag6ToneMappedImageGreenChannelSkinBasedBlackPoint"`
	Tag6ToneMappedImageGreenChannelSkinBasedHighKey    float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedHighKey"`
	Tag6ToneMappedImageGreenChannelSkinBasedP02        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP02"`
	Tag6ToneMappedImageGreenChannelSkinBasedP10        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP10"`
	Tag6ToneMappedImageGreenChannelSkinBasedP25        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP25"`
	Tag6ToneMappedImageGreenChannelSkinBasedP50        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP50"`
	Tag6ToneMappedImageGreenChannelSkinBasedP75        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP75"`
	Tag6ToneMappedImageGreenChannelSkinBasedP98        float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedP98"`
	Tag6ToneMappedImageGreenChannelSkinBasedWhitePoint float64   `json:"Tag6ToneMappedImageGreenChannelSkinBasedWhitePoint"`
	Tag6ToneMappedImageHighKey                         float64   `json:"Tag6ToneMappedImageHighKey"`
	Tag6ToneMappedImageP02                             string    `json:"Tag6ToneMappedImageP02"`
	Tag6ToneMappedImageP10                             float64   `json:"Tag6ToneMappedImageP10"`
	Tag6ToneMappedImageP25                             float64   `json:"Tag6ToneMappedImageP25"`
	Tag6ToneMappedImageP50                             float64   `json:"Tag6ToneMappedImageP50"`
	Tag6ToneMappedImageP75                             float64   `json:"Tag6ToneMappedImageP75"`
	Tag6ToneMappedImageP98                             float64   `json:"Tag6ToneMappedImageP98"`
	Tag6ToneMappedImagePersonSegmentBasedBlackPoint    int       `json:"Tag6ToneMappedImagePersonSegmentBasedBlackPoint"`
	Tag6ToneMappedImagePersonSegmentBasedHighKey       float64   `json:"Tag6ToneMappedImagePersonSegmentBasedHighKey"`
	Tag6ToneMappedImagePersonSegmentBasedP02           string    `json:"Tag6ToneMappedImagePersonSegmentBasedP02"`
	Tag6ToneMappedImagePersonSegmentBasedP10           string    `json:"Tag6ToneMappedImagePersonSegmentBasedP10"`
	Tag6ToneMappedImagePersonSegmentBasedP25           string    `json:"Tag6ToneMappedImagePersonSegmentBasedP25"`
	Tag6ToneMappedImagePersonSegmentBasedP50           float64   `json:"Tag6ToneMappedImagePersonSegmentBasedP50"`
	Tag6ToneMappedImagePersonSegmentBasedP75           float64   `json:"Tag6ToneMappedImagePersonSegmentBasedP75"`
	Tag6ToneMappedImagePersonSegmentBasedP98           float64   `json:"Tag6ToneMappedImagePersonSegmentBasedP98"`
	Tag6ToneMappedImagePersonSegmentBasedWhitePoint    float64   `json:"Tag6ToneMappedImagePersonSegmentBasedWhitePoint"`
	Tag6ToneMappedImageRedChannelSkinBasedBlackPoint   string    `json:"Tag6ToneMappedImageRedChannelSkinBasedBlackPoint"`
	Tag6ToneMappedImageRedChannelSkinBasedHighKey      float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedHighKey"`
	Tag6ToneMappedImageRedChannelSkinBasedP02          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP02"`
	Tag6ToneMappedImageRedChannelSkinBasedP10          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP10"`
	Tag6ToneMappedImageRedChannelSkinBasedP25          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP25"`
	Tag6ToneMappedImageRedChannelSkinBasedP50          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP50"`
	Tag6ToneMappedImageRedChannelSkinBasedP75          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP75"`
	Tag6ToneMappedImageRedChannelSkinBasedP98          float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedP98"`
	Tag6ToneMappedImageRedChannelSkinBasedWhitePoint   float64   `json:"Tag6ToneMappedImageRedChannelSkinBasedWhitePoint"`
	Tag6ToneMappedImageSkinBasedBlackPoint             string    `json:"Tag6ToneMappedImageSkinBasedBlackPoint"`
	Tag6ToneMappedImageSkinBasedHighKey                float64   `json:"Tag6ToneMappedImageSkinBasedHighKey"`
	Tag6ToneMappedImageSkinBasedP02                    float64   `json:"Tag6ToneMappedImageSkinBasedP02"`
	Tag6ToneMappedImageSkinBasedP10                    float64   `json:"Tag6ToneMappedImageSkinBasedP10"`
	Tag6ToneMappedImageSkinBasedP25                    float64   `json:"Tag6ToneMappedImageSkinBasedP25"`
	Tag6ToneMappedImageSkinBasedP50                    float64   `json:"Tag6ToneMappedImageSkinBasedP50"`
	Tag6ToneMappedImageSkinBasedP75                    float64   `json:"Tag6ToneMappedImageSkinBasedP75"`
	Tag6ToneMappedImageSkinBasedP98                    float64   `json:"Tag6ToneMappedImageSkinBasedP98"`
	Tag6ToneMappedImageSkinBasedWhitePoint             float64   `json:"Tag6ToneMappedImageSkinBasedWhitePoint"`
	Tag6ToneMappedImageWhitePoint                      float64   `json:"Tag6ToneMappedImageWhitePoint"`
	Tag7PeopleRatio                                    float64   `json:"Tag7PeopleRatio"`
	Tag7PersonMasksValidHint                           int       `json:"Tag7PersonMasksValidHint"`
	Tag7SkinRatio                                      float64   `json:"Tag7SkinRatio"`
	TagC                                               string    `json:"TagC"`
	TagD                                               string    `json:"TagD"`
	TagE                                               int       `json:"TagE"`
	TagF                                               int       `json:"TagF"`
	TagG                                               int       `json:"TagG"`
	TagH                                               float64   `json:"TagH"`
	TagJ                                               float64   `json:"TagJ"`
	TagK                                               bool      `json:"TagK"`
	TemporalIDNested                                   int       `json:"TemporalIDNested"`
	ThumbnailImage                                     string    `json:"ThumbnailImage"`
	ThumbnailLength                                    int       `json:"ThumbnailLength"`
	ThumbnailOffset                                    int       `json:"ThumbnailOffset"`
	TileLength                                         int       `json:"TileLength"`
	TileWidth                                          int       `json:"TileWidth"`
	TimeStamp                                          string    `json:"TimeStamp"`
	TransferCharacteristics                            int       `json:"TransferCharacteristics"`
	UniformResourceName                                string    `json:"UniformResourceName"`
	Version                                            int       `json:"Version"`
	VideoFullRangeFlag                                 int       `json:"VideoFullRangeFlag"`
	Warning                                            string    `json:"Warning"`
	WhiteBalance                                       int       `json:"WhiteBalance"`
	XMPToolkit                                         string    `json:"XMPToolkit"`
	XResolution                                        int       `json:"XResolution"`
	YCbCrPositioning                                   int       `json:"YCbCrPositioning"`
	YCbCrSubSampling                                   string    `json:"YCbCrSubSampling"`
	YResolution                                        int       `json:"YResolution"`
}

func (Heic) MediaType() string { return MediaTypeImage }

func (h Heic) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:      ftoa(h.ExifToolVersion),
		SourceFile:           h.SourceFile,
		Directory:            h.Directory,
		FileName:             h.FileName,
		FileSize:             itoa(h.FileSize),
		FilePermissions:      itoa(h.FilePermissions),
		FileType:             h.FileType,
		FileTypeExtension:    h.FileTypeExtension,
		MIMEType:             h.MIMEType,
		FileModifyDate:       h.FileModifyDate,
		FileAccessDate:       h.FileAccessDate,
		FileInodeChangeDate:  h.FileInodeChangeDate,
		ImageWidth:           itoa(h.ImageWidth),
		ImageHeight:          itoa(h.ImageHeight),
		ImageSize:            h.ImageSize,
		Megapixels:           ftoa(h.Megapixels),
		Orientation:          itoa(h.Orientation),
		Make:                 h.Make,
		Model:                h.Model,
		LensModel:            h.LensModel,
		Software:             ftoa(h.Software),
		CreateDate:           h.CreateDate,
		ModifyDate:           h.ModifyDate,
		DateTimeOriginal:     h.DateTimeOriginal,
		ISO:                  itoa(h.Iso),
		Aperture:             ftoa(h.Aperture),
		FNumber:              ftoa(h.FNumber),
		FocalLength:          ftoa(h.FocalLength),
		ExposureTime:         ftoa(h.ExposureTime),
		ShutterSpeed:         ftoa(h.ShutterSpeed),
		ExposureMode:         itoa(h.ExposureMode),
		ExposureProgram:      itoa(h.ExposureProgram),
		ExposureCompensation: itoa(h.ExposureCompensation),
		Flash:                itoa(h.Flash),
		MeteringMode:         itoa(h.MeteringMode),
		WhiteBalance:         itoa(h.WhiteBalance),
		GPSLatitude:          ftoa(h.GPSLatitude),
		GPSLongitude:         ftoa(h.GPSLongitude),
		GPSAltitude:          ftoa(h.GPSAltitude),
		GPSAltitudeRef:       itoa(h.GPSAltitudeRef),
		GPSPosition:          h.GPSPosition,
	}
}
