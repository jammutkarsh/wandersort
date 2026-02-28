package classifier

type Aae struct {
	AdjustmentBaseVersion                                                                 int       `json:"AdjustmentBaseVersion"`
	AdjustmentEditorBundleID                                                              string    `json:"AdjustmentEditorBundleID"`
	AdjustmentFormatIdentifier                                                            string    `json:"AdjustmentFormatIdentifier"`
	AdjustmentFormatVersion                                                               float64   `json:"AdjustmentFormatVersion"`
	AdjustmentRenderTypes                                                                 int       `json:"AdjustmentRenderTypes"`
	AdjustmentTimestamp                                                                   string    `json:"AdjustmentTimestamp"`
	AdjustmentsEnabled                                                                    bool      `json:"AdjustmentsEnabled"`
	AdjustmentsFormatIdentifier                                                           string    `json:"AdjustmentsFormatIdentifier"`
	AdjustmentsFormatVersion                                                              int       `json:"AdjustmentsFormatVersion"`
	AdjustmentsIdentifier                                                                 string    `json:"AdjustmentsIdentifier"`
	AdjustmentsSettingsAlignmentExtent                                                    []int     `json:"AdjustmentsSettingsAlignmentExtent"`
	AdjustmentsSettingsAlignmentTransform                                                 []any     `json:"AdjustmentsSettingsAlignmentTransform"`
	AdjustmentsSettingsAperture                                                           float64   `json:"AdjustmentsSettingsAperture"`
	AdjustmentsSettingsAuto                                                               bool      `json:"AdjustmentsSettingsAuto"`
	AdjustmentsSettingsCast                                                               string    `json:"AdjustmentsSettingsCast"`
	AdjustmentsSettingsColor                                                              string    `json:"AdjustmentsSettingsColor"`
	AdjustmentsSettingsConstraintHeight                                                   int       `json:"AdjustmentsSettingsConstraintHeight"`
	AdjustmentsSettingsConstraintWidth                                                    int       `json:"AdjustmentsSettingsConstraintWidth"`
	AdjustmentsSettingsDepthInfoCapturedAperture                                          float64   `json:"AdjustmentsSettingsDepthInfoCapturedAperture"`
	AdjustmentsSettingsDepthInfoFacesChinX                                                string    `json:"AdjustmentsSettingsDepthInfoFacesChinX"`
	AdjustmentsSettingsDepthInfoFacesChinY                                                string    `json:"AdjustmentsSettingsDepthInfoFacesChinY"`
	AdjustmentsSettingsDepthInfoFacesLeftEyeX                                             string    `json:"AdjustmentsSettingsDepthInfoFacesLeftEyeX"`
	AdjustmentsSettingsDepthInfoFacesLeftEyeY                                             string    `json:"AdjustmentsSettingsDepthInfoFacesLeftEyeY"`
	AdjustmentsSettingsDepthInfoFacesNoseX                                                string    `json:"AdjustmentsSettingsDepthInfoFacesNoseX"`
	AdjustmentsSettingsDepthInfoFacesNoseY                                                string    `json:"AdjustmentsSettingsDepthInfoFacesNoseY"`
	AdjustmentsSettingsDepthInfoFacesRightEyeX                                            string    `json:"AdjustmentsSettingsDepthInfoFacesRightEyeX"`
	AdjustmentsSettingsDepthInfoFacesRightEyeY                                            string    `json:"AdjustmentsSettingsDepthInfoFacesRightEyeY"`
	AdjustmentsSettingsDepthInfoFocusRectH                                                string    `json:"AdjustmentsSettingsDepthInfoFocusRectH"`
	AdjustmentsSettingsDepthInfoFocusRectW                                                string    `json:"AdjustmentsSettingsDepthInfoFocusRectW"`
	AdjustmentsSettingsDepthInfoFocusRectX                                                string    `json:"AdjustmentsSettingsDepthInfoFocusRectX"`
	AdjustmentsSettingsDepthInfoFocusRectY                                                string    `json:"AdjustmentsSettingsDepthInfoFocusRectY"`
	AdjustmentsSettingsDepthInfoLumaNoiseScale                                            string    `json:"AdjustmentsSettingsDepthInfoLumaNoiseScale"`
	AdjustmentsSettingsDepthInfoMaximumAperture                                           int       `json:"AdjustmentsSettingsDepthInfoMaximumAperture"`
	AdjustmentsSettingsDepthInfoMinimumAperture                                           float64   `json:"AdjustmentsSettingsDepthInfoMinimumAperture"`
	AdjustmentsSettingsDepthInfoSDOFRenderingVersion                                      int       `json:"AdjustmentsSettingsDepthInfoSDOFRenderingVersion"`
	AdjustmentsSettingsDialogMixBias                                                      float64   `json:"AdjustmentsSettingsDialogMixBias"`
	AdjustmentsSettingsEffectIntensity                                                    int       `json:"AdjustmentsSettingsEffectIntensity"`
	AdjustmentsSettingsEffectName                                                         string    `json:"AdjustmentsSettingsEffectName"`
	AdjustmentsSettingsEffectVersion                                                      int       `json:"AdjustmentsSettingsEffectVersion"`
	AdjustmentsSettingsEnabled                                                            []bool    `json:"AdjustmentsSettingsEnabled"`
	AdjustmentsSettingsEndTimeEpoch                                                       int       `json:"AdjustmentsSettingsEndTimeEpoch"`
	AdjustmentsSettingsEndTimeFlags                                                       int       `json:"AdjustmentsSettingsEndTimeFlags"`
	AdjustmentsSettingsEndTimeTimescale                                                   int       `json:"AdjustmentsSettingsEndTimeTimescale"`
	AdjustmentsSettingsEndTimeValue                                                       int       `json:"AdjustmentsSettingsEndTimeValue"`
	AdjustmentsSettingsFlavor                                                             string    `json:"AdjustmentsSettingsFlavor"`
	AdjustmentsSettingsFocusRectH                                                         string    `json:"AdjustmentsSettingsFocusRectH"`
	AdjustmentsSettingsFocusRectW                                                         string    `json:"AdjustmentsSettingsFocusRectW"`
	AdjustmentsSettingsFocusRectX                                                         string    `json:"AdjustmentsSettingsFocusRectX"`
	AdjustmentsSettingsFocusRectY                                                         string    `json:"AdjustmentsSettingsFocusRectY"`
	AdjustmentsSettingsGlassesMatteAllowed                                                bool      `json:"AdjustmentsSettingsGlassesMatteAllowed"`
	AdjustmentsSettingsHeight                                                             int       `json:"AdjustmentsSettingsHeight"`
	AdjustmentsSettingsInputColor                                                         string    `json:"AdjustmentsSettingsInputColor"`
	AdjustmentsSettingsInputKeysInputEdgeDetail                                           float64   `json:"AdjustmentsSettingsInputKeysInputEdgeDetail"`
	AdjustmentsSettingsInputKeysInputEdgeScale                                            int       `json:"AdjustmentsSettingsInputKeysInputEdgeScale"`
	AdjustmentsSettingsInputKeysInputFalloff                                              []int     `json:"AdjustmentsSettingsInputKeysInputFalloff"`
	AdjustmentsSettingsInputKeysInputIntensity                                            []any     `json:"AdjustmentsSettingsInputKeysInputIntensity"`
	AdjustmentsSettingsInputKeysInputRadius                                               []any     `json:"AdjustmentsSettingsInputKeysInputRadius"`
	AdjustmentsSettingsInputKeysInputSharpness                                            int       `json:"AdjustmentsSettingsInputKeysInputSharpness"`
	AdjustmentsSettingsInputLight                                                         string    `json:"AdjustmentsSettingsInputLight"`
	AdjustmentsSettingsIntensity                                                          int       `json:"AdjustmentsSettingsIntensity"`
	AdjustmentsSettingsOffsetBlackPoint                                                   string    `json:"AdjustmentsSettingsOffsetBlackPoint"`
	AdjustmentsSettingsOffsetBrightness                                                   string    `json:"AdjustmentsSettingsOffsetBrightness"`
	AdjustmentsSettingsOffsetCast                                                         int       `json:"AdjustmentsSettingsOffsetCast"`
	AdjustmentsSettingsOffsetContrast                                                     []string  `json:"AdjustmentsSettingsOffsetContrast"`
	AdjustmentsSettingsOffsetExposure                                                     string    `json:"AdjustmentsSettingsOffsetExposure"`
	AdjustmentsSettingsOffsetHighlights                                                   string    `json:"AdjustmentsSettingsOffsetHighlights"`
	AdjustmentsSettingsOffsetLocalLight                                                   string    `json:"AdjustmentsSettingsOffsetLocalLight"`
	AdjustmentsSettingsOffsetSaturation                                                   string    `json:"AdjustmentsSettingsOffsetSaturation"`
	AdjustmentsSettingsOffsetShadows                                                      string    `json:"AdjustmentsSettingsOffsetShadows"`
	AdjustmentsSettingsPitch                                                              int       `json:"AdjustmentsSettingsPitch"`
	AdjustmentsSettingsPortraitEffectFilterName                                           string    `json:"AdjustmentsSettingsPortraitEffectFilterName"`
	AdjustmentsSettingsPortraitInfoCapturedPortraitStrength                               float64   `json:"AdjustmentsSettingsPortraitInfoCapturedPortraitStrength"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksAllPointsX                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksAllPointsX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksAllPointsY                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksAllPointsY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxH                          string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxH"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxW                          string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxW"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxX                          string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxY                          string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceBoundingBoxY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceContourX                              []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceContourX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceContourY                              []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceContourY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceJunkinessIndex                        float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceJunkinessIndex"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksFaceOrientationIndex                      float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksFaceOrientationIndex"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksInnerLipsX                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksInnerLipsX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksInnerLipsY                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksInnerLipsY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyeX                                  []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyeX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyeY                                  []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyeY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyebrowX                              []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyebrowX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyebrowY                              []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftEyebrowY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftPupilX                                float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftPupilX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksLeftPupilY                                float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksLeftPupilY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksMedianLineX                               []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksMedianLineX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksMedianLineY                               []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksMedianLineY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksNoseCrestX                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksNoseCrestX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksNoseCrestY                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksNoseCrestY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksNoseX                                     []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksNoseX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksNoseY                                     []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksNoseY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksOrientation                               int       `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksOrientation"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksOuterLipsX                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksOuterLipsX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksOuterLipsY                                []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksOuterLipsY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyeX                                 []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyeX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyeY                                 []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyeY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyebrowX                             []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyebrowX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyebrowY                             []float64 `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightEyebrowY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightPupilX                               float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightPupilX"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRightPupilY                               float64   `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRightPupilY"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksRoll                                      string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksRoll"`
	AdjustmentsSettingsPortraitInfoFaceLandmarksYaw                                       string    `json:"AdjustmentsSettingsPortraitInfoFaceLandmarksYaw"`
	AdjustmentsSettingsRecipeAutoLoopErrorCode                                            int       `json:"AdjustmentsSettingsRecipeAutoLoopErrorCode"`
	AdjustmentsSettingsRecipeAutoLoopParamsLoopEnergy                                     string    `json:"AdjustmentsSettingsRecipeAutoLoopParamsLoopEnergy"`
	AdjustmentsSettingsRecipeAutoLoopParamsLoopFadeLen                                    int       `json:"AdjustmentsSettingsRecipeAutoLoopParamsLoopFadeLen"`
	AdjustmentsSettingsRecipeAutoLoopParamsLoopFlavor                                     string    `json:"AdjustmentsSettingsRecipeAutoLoopParamsLoopFlavor"`
	AdjustmentsSettingsRecipeAutoLoopParamsLoopPeriod                                     int       `json:"AdjustmentsSettingsRecipeAutoLoopParamsLoopPeriod"`
	AdjustmentsSettingsRecipeAutoLoopParamsLoopStart                                      int       `json:"AdjustmentsSettingsRecipeAutoLoopParamsLoopStart"`
	AdjustmentsSettingsRecipeBounceErrorCode                                              int       `json:"AdjustmentsSettingsRecipeBounceErrorCode"`
	AdjustmentsSettingsRecipeBounceParamsLoopEnergy                                       float64   `json:"AdjustmentsSettingsRecipeBounceParamsLoopEnergy"`
	AdjustmentsSettingsRecipeBounceParamsLoopFadeLen                                      int       `json:"AdjustmentsSettingsRecipeBounceParamsLoopFadeLen"`
	AdjustmentsSettingsRecipeBounceParamsLoopFlavor                                       string    `json:"AdjustmentsSettingsRecipeBounceParamsLoopFlavor"`
	AdjustmentsSettingsRecipeBounceParamsLoopPeriod                                       int       `json:"AdjustmentsSettingsRecipeBounceParamsLoopPeriod"`
	AdjustmentsSettingsRecipeBounceParamsLoopStart                                        int       `json:"AdjustmentsSettingsRecipeBounceParamsLoopStart"`
	AdjustmentsSettingsRecipeLongExposureErrorCode                                        int       `json:"AdjustmentsSettingsRecipeLongExposureErrorCode"`
	AdjustmentsSettingsRecipeLongExposureParamsLoopEnergy                                 int       `json:"AdjustmentsSettingsRecipeLongExposureParamsLoopEnergy"`
	AdjustmentsSettingsRecipeLongExposureParamsLoopFadeLen                                int       `json:"AdjustmentsSettingsRecipeLongExposureParamsLoopFadeLen"`
	AdjustmentsSettingsRecipeLongExposureParamsLoopFlavor                                 string    `json:"AdjustmentsSettingsRecipeLongExposureParamsLoopFlavor"`
	AdjustmentsSettingsRecipeLongExposureParamsLoopPeriod                                 int       `json:"AdjustmentsSettingsRecipeLongExposureParamsLoopPeriod"`
	AdjustmentsSettingsRecipeLongExposureParamsLoopStart                                  int       `json:"AdjustmentsSettingsRecipeLongExposureParamsLoopStart"`
	AdjustmentsSettingsRecipeMinVersion                                                   int       `json:"AdjustmentsSettingsRecipeMinVersion"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsGapBridgeGapLength []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsGapBridgeGapLength"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsGapBridgeGapStart  []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsGapBridgeGapStart"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsHomography         []any     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsHomography"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeEpoch       []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeEpoch"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeFlags       []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeFlags"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeTimescale   []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeTimescale"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeValue       []int     `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsFrameInstructionsRawTimeValue"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurEpoch                 int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurEpoch"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurFlags                 int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurFlags"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurTimescale             int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurTimescale"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurValue                 int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsOutputFrameDurValue"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectHeight                  int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectHeight"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectWidth                   int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectWidth"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectX                       int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectX"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectY                       int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsStabCropRectY"`
	AdjustmentsSettingsRecipeNormStabilizeInstructionsStabilizeResult                     int       `json:"AdjustmentsSettingsRecipeNormStabilizeInstructionsStabilizeResult"`
	AdjustmentsSettingsRecipeVersion                                                      int       `json:"AdjustmentsSettingsRecipeVersion"`
	AdjustmentsSettingsRenderingStyle                                                     string    `json:"AdjustmentsSettingsRenderingStyle"`
	AdjustmentsSettingsRenderingVersion                                                   int       `json:"AdjustmentsSettingsRenderingVersion"`
	AdjustmentsSettingsRenderingVersionAtCapture                                          int       `json:"AdjustmentsSettingsRenderingVersionAtCapture"`
	AdjustmentsSettingsSpillMatteAllowed                                                  bool      `json:"AdjustmentsSettingsSpillMatteAllowed"`
	AdjustmentsSettingsStartTimeEpoch                                                     int       `json:"AdjustmentsSettingsStartTimeEpoch"`
	AdjustmentsSettingsStartTimeFlags                                                     int       `json:"AdjustmentsSettingsStartTimeFlags"`
	AdjustmentsSettingsStartTimeTimescale                                                 int       `json:"AdjustmentsSettingsStartTimeTimescale"`
	AdjustmentsSettingsStartTimeValue                                                     int       `json:"AdjustmentsSettingsStartTimeValue"`
	AdjustmentsSettingsStatisticsAutoValue                                                string    `json:"AdjustmentsSettingsStatisticsAutoValue"`
	AdjustmentsSettingsStatisticsBlackPoint                                               string    `json:"AdjustmentsSettingsStatisticsBlackPoint"`
	AdjustmentsSettingsStatisticsHighKey                                                  float64   `json:"AdjustmentsSettingsStatisticsHighKey"`
	AdjustmentsSettingsStatisticsLightMap                                                 string    `json:"AdjustmentsSettingsStatisticsLightMap"`
	AdjustmentsSettingsStatisticsLightMapAvg                                              string    `json:"AdjustmentsSettingsStatisticsLightMapAvg"`
	AdjustmentsSettingsStatisticsLightMapHeight                                           int       `json:"AdjustmentsSettingsStatisticsLightMapHeight"`
	AdjustmentsSettingsStatisticsLightMapWidth                                            int       `json:"AdjustmentsSettingsStatisticsLightMapWidth"`
	AdjustmentsSettingsStatisticsLocalAutoValue                                           string    `json:"AdjustmentsSettingsStatisticsLocalAutoValue"`
	AdjustmentsSettingsStatisticsP02                                                      string    `json:"AdjustmentsSettingsStatisticsP02"`
	AdjustmentsSettingsStatisticsP10                                                      string    `json:"AdjustmentsSettingsStatisticsP10"`
	AdjustmentsSettingsStatisticsP25                                                      string    `json:"AdjustmentsSettingsStatisticsP25"`
	AdjustmentsSettingsStatisticsP50                                                      string    `json:"AdjustmentsSettingsStatisticsP50"`
	AdjustmentsSettingsStatisticsP98                                                      string    `json:"AdjustmentsSettingsStatisticsP98"`
	AdjustmentsSettingsStatisticsSatAutoValue                                             string    `json:"AdjustmentsSettingsStatisticsSatAutoValue"`
	AdjustmentsSettingsStatisticsSatPercentile75                                          string    `json:"AdjustmentsSettingsStatisticsSatPercentile75"`
	AdjustmentsSettingsStatisticsSatPercentile98                                          string    `json:"AdjustmentsSettingsStatisticsSatPercentile98"`
	AdjustmentsSettingsStatisticsSatPercentileG98                                         string    `json:"AdjustmentsSettingsStatisticsSatPercentileG98"`
	AdjustmentsSettingsStatisticsTonalRange                                               string    `json:"AdjustmentsSettingsStatisticsTonalRange"`
	AdjustmentsSettingsStatisticsWhitePoint                                               float64   `json:"AdjustmentsSettingsStatisticsWhitePoint"`
	AdjustmentsSettingsStraightenAngle                                                    int       `json:"AdjustmentsSettingsStraightenAngle"`
	AdjustmentsSettingsStrength                                                           float64   `json:"AdjustmentsSettingsStrength"`
	AdjustmentsSettingsTone                                                               string    `json:"AdjustmentsSettingsTone"`
	AdjustmentsSettingsVersion                                                            int       `json:"AdjustmentsSettingsVersion"`
	AdjustmentsSettingsWidth                                                              int       `json:"AdjustmentsSettingsWidth"`
	AdjustmentsSettingsXOrigin                                                            int       `json:"AdjustmentsSettingsXOrigin"`
	AdjustmentsSettingsYOrigin                                                            int       `json:"AdjustmentsSettingsYOrigin"`
	AdjustmentsSettingsYaw                                                                int       `json:"AdjustmentsSettingsYaw"`
	Archiver                                                                              string    `json:"Archiver"`
	Directory                                                                             string    `json:"Directory"`
	ExifToolVersion                                                                       float64   `json:"ExifToolVersion"`
	FileAccessDate                                                                        string    `json:"FileAccessDate"`
	FileInodeChangeDate                                                                   string    `json:"FileInodeChangeDate"`
	FileModifyDate                                                                        string    `json:"FileModifyDate"`
	FileName                                                                              string    `json:"FileName"`
	FilePermissions                                                                       int       `json:"FilePermissions"`
	FileSize                                                                              int       `json:"FileSize"`
	FileType                                                                              string    `json:"FileType"`
	FileTypeExtension                                                                     string    `json:"FileTypeExtension"`
	FormatVersion                                                                         int       `json:"FormatVersion"`
	MIMEType                                                                              string    `json:"MIMEType"`
	MetadataMasterHeight                                                                  int       `json:"MetadataMasterHeight"`
	MetadataMasterWidth                                                                   int       `json:"MetadataMasterWidth"`
	MetadataOrientation                                                                   int       `json:"MetadataOrientation"`
	Objects                                                                               []string  `json:"Objects"`
	ObjectsClass                                                                          int       `json:"ObjectsClass"`
	ObjectsClasses                                                                        []string  `json:"ObjectsClasses"`
	ObjectsClassname                                                                      string    `json:"ObjectsClassname"`
	SourceFile                                                                            string    `json:"SourceFile"`
	TopRoot                                                                               int       `json:"TopRoot"`
	Version                                                                               int       `json:"Version"`
	VersionInfoAppVersion                                                                 string    `json:"VersionInfoAppVersion"`
	VersionInfoBuildNumber                                                                string    `json:"VersionInfoBuildNumber"`
	VersionInfoPlatform                                                                   string    `json:"VersionInfoPlatform"`
	VersionInfoSchemaRevision                                                             int       `json:"VersionInfoSchemaRevision"`
}

func (Aae) MediaType() string { return MediaTypeSidecar }

func (a Aae) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:     ftoa(a.ExifToolVersion),
		SourceFile:          a.SourceFile,
		Directory:           a.Directory,
		FileName:            a.FileName,
		FileSize:            itoa(a.FileSize),
		FilePermissions:     itoa(a.FilePermissions),
		FileType:            a.FileType,
		FileTypeExtension:   a.FileTypeExtension,
		MIMEType:            a.MIMEType,
		FileModifyDate:      a.FileModifyDate,
		FileAccessDate:      a.FileAccessDate,
		FileInodeChangeDate: a.FileInodeChangeDate,
	}
}
