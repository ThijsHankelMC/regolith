package regolith

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
)

// Install handles the "regolith install" command. It installs specific filters
// from the Internet and adds them to the filtersDefinitions list in the
// config.json file.
//
// The "filters" parameter is a list of filters to install in the format
// <filter-url>==<filter-version> or <filter-url>.
// "filter-url" is the URL of the filter to install.
// "filter-version" is the version of the filter. It can be semver, git commit
// hash, "HEAD", or "latest". "HEAD" means that the filter will be
// updated to lastest SHA commit and "latest" updates the filter to the latest
// version tag. If "filter-version" is not specified, the filter will be
// installed with the latest version or HEAD if there is no valid version tags.
//
// The "force" parameter is a boolean that determines if the installation
// should be forced even if the filter is already installed.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func Install(filters []string, force, debug bool) error {
	InitLogging(debug)
	Logger.Info("Installing filters...")
	if !hasGit() {
		Logger.Warn(gitNotInstalledWarning)
	}
	// Parse arguments into download tasks
	parsedArgs, err := parseInstallFilterArgs(filters)
	if err != nil {
		return WrapError(err, "Failed to parse arguments.")
	}
	config, err := LoadConfigAsMap()
	if err != nil {
		return WrapError(err, "Unable to load config file.")
	}
	// Get parts of config file required for installation
	dataPath, err := dataPathFromConfigMap(config)
	if err != nil {
		return WrapError(err, "Failed to get data path from config file.")
	}
	filterDefinitions, err := filterDefinitionsFromConfigMap(config)
	if err != nil {
		return WrapError(
			err,
			"Failed to get the list of filter definitions from config file.")
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() { sessionLockErr = unlockSession() }()
	// Check if the filters are already installed if force mode is disabled
	if !force {
		for _, parsedArg := range parsedArgs {
			_, ok := filterDefinitions[parsedArg.name]
			if ok {
				return WrappedErrorf(
					"The filter is already on the filter definitions list.\n"+
						"Filter: %s\n"+
						"If you want to force the installation of the filter, "+
						"please add \"--force\" flag to your "+
						"\"regolith install\" command", parsedArg.name)
			}
		}
	}
	// Convert to filter definitions for download
	filterInstallers := make(map[string]FilterInstaller, 0)
	for _, parsedArg := range parsedArgs {
		// Get the filter definition from the Internet
		remoteFilterDefinition, err := FilterDefinitionFromTheInternet(
			parsedArg.url, parsedArg.name, parsedArg.version)
		if err != nil {
			return WrapErrorf(
				err,
				"Unable to download the filter definition from the Internet.\n"+
					"Filter repository Url: %s\n"+
					"Filter name: %s\n"+
					"Filter version: %s\n",
				parsedArg.url, parsedArg.name, parsedArg.version)
		}
		if parsedArg.version == "HEAD" || parsedArg.version == "latest" {
			// The "HEAD" and "latest" keywords should be the same in the
			// config file don't lock them to the actual versions
			remoteFilterDefinition.Version = parsedArg.version
		}
		filterInstallers[parsedArg.name] = remoteFilterDefinition
	}
	// Download the filter definitions
	err = installFilters(filterInstallers, force, dataPath, dotRegolithPath)
	if err != nil {
		return WrapError(err, "Failed to install filters.")
	}
	// Add the filters to the config
	for name, downloadedFilter := range filterInstallers {
		// Add the filter to config file
		filterDefinitions[name] = downloadedFilter
	}
	// Save the config file
	jsonBytes, _ := json.MarshalIndent(config, "", "\t")
	err = ioutil.WriteFile(ConfigFilePath, jsonBytes, 0644)
	if err != nil {
		return WrapErrorf(
			err,
			"Successfully downloaded %v filters"+
				"but failed to update the config file.\n"+
				"Run \"regolith clean\" to fix invalid cache state.",
			len(parsedArgs))
	}
	Logger.Info("Successfully installed the filters.")
	return sessionLockErr // Return the error from the defer function
}

// InstallAll handles the "regolith install-all" command. It installs all of
// filters and their dependencies from the filtersDefinitions list in the
// config.json file.
//
// The "force" parameter is a boolean that determines if the installation
// should be forced even if the filter is already installed.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func InstallAll(force, debug bool) error {
	InitLogging(debug)
	Logger.Info("Installing filters...")
	if !hasGit() {
		Logger.Warn(gitNotInstalledWarning)
	}
	configMap, err1 := LoadConfigAsMap()
	config, err2 := ConfigFromObject(configMap)
	if err := firstErr(err1, err2); err != nil {
		return WrapError(err, "Failed to load config.json.")
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() { sessionLockErr = unlockSession() }()
	// Install the filters
	err = installFilters(
		config.FilterDefinitions, force, config.DataPath, dotRegolithPath)
	if err != nil {
		return WrapError(err, "Could not install filters.")
	}
	Logger.Info("Successfully installed the filters.")
	return sessionLockErr // Return the error from the defer function
}

// Update handles the "regolith update" command. It updates filters listed in
// "filters" parameter. The names of the filters must be already present in the
// filtersDefinitions list in the config.json file.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func Update(filters []string, debug bool) error {
	InitLogging(debug)
	Logger.Info("Updating filters...")
	if !hasGit() {
		Logger.Warn(gitNotInstalledWarning)
	}
	if len(filters) == 0 {
		return WrappedError("No filters specified.")
	}
	configMap, err1 := LoadConfigAsMap()
	config, err2 := ConfigFromObject(configMap)
	if err := firstErr(err1, err2); err != nil {
		return WrapError(err, "Failed to load config.json.")
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() { sessionLockErr = unlockSession() }()
	// Filter out the filters that are not present in the 'filters' list
	filterInstallers := make(map[string]FilterInstaller, 0)
	for _, filterName := range filters {
		filterInstaller, ok := config.FilterDefinitions[filterName]
		if !ok {
			Logger.Warnf(
				"Filter %q is not installed and therefore cannot be updated.",
				filterName)
			continue
		}
		filterInstallers[filterName] = filterInstaller
	}
	// Update the filters from the list
	err = updateFilters(filterInstallers, dotRegolithPath)
	if err != nil {
		return WrapError(err, "Could not update filters.")
	}
	Logger.Info("Successfully updated the filters.")
	return sessionLockErr // Return the error from the defer function
}

// UpdateAll handles the "regolith update-all" command. It updates all of the
// filters from the filtersDefinitions list in the config.json file which
// aren't version locked.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func UpdateAll(debug bool) error {
	InitLogging(debug)
	Logger.Info("Updating filters...")
	if !hasGit() {
		Logger.Warn(gitNotInstalledWarning)
	}
	configMap, err1 := LoadConfigAsMap()
	config, err2 := ConfigFromObject(configMap)
	if err := firstErr(err1, err2); err != nil {
		return WrapError(err, "Failed to load config.json.")
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() { sessionLockErr = unlockSession() }()
	// Update the filters
	err = updateFilters(config.FilterDefinitions, dotRegolithPath)
	if err != nil {
		return WrapError(err, "Could not install filters.")
	}
	Logger.Info("Successfully installed the filters.")
	return sessionLockErr // Return the error from the defer function
}

// runOrWatch handles both 'regolith run' and 'regolith watch' commands based
// on the 'watch' parameter. It runs/watches the profile named after
// 'profileName' parameter. The 'debug' argument determines if the debug
// messages should be printed or not.
func runOrWatch(profileName string, debug, watch bool) error {
	InitLogging(debug)
	if profileName == "" {
		profileName = "default"
	}
	// Load the Config and the profile
	configJson, err := LoadConfigAsMap()
	if err != nil {
		return WrapError(err, "Could not load \"config.json\".")
	}
	config, err := ConfigFromObject(configJson)
	if err != nil {
		return WrapError(err, "Could not load \"config.json\".")
	}
	profile, ok := config.Profiles[profileName]
	if !ok {
		return WrappedErrorf(
			"Profile %q does not exist in the configuration.", profileName)
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	err = CreateDirectoryIfNotExists(dotRegolithPath)
	if err != nil {
		return WrapErrorf(err, osMkdirError, dotRegolithPath)
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() { sessionLockErr = unlockSession() }()
	// Check the filters of the profile
	err = CheckProfileImpl(profile, profileName, *config, nil, dotRegolithPath)
	if err != nil {
		return err
	}
	path, _ := filepath.Abs(".")
	context := RunContext{
		AbsoluteLocation: path,
		Config:           config,
		Parent:           nil,
		Profile:          profileName,
		DotRegolithPath:  dotRegolithPath,
	}
	if watch { // Loop until program termination (CTRL+C)
		context.StartWatchingSourceFiles()
		for {
			err = RunProfile(context)
			if err != nil {
				Logger.Errorf(
					"Failed to run profile %q: %s",
					profileName, PassError(err).Error())
			} else {
				Logger.Infof("Successfully ran the %q profile.", profileName)
			}
			Logger.Info("Press Ctrl+C to stop watching.")
			context.AwaitInterruption()
			Logger.Warn("Restarting...")
		}
		// return nil // Unreachable code
	}
	err = RunProfile(context)
	if err != nil {
		return WrapErrorf(err, "Failed to run profile %q", profileName)
	}
	Logger.Infof("Successfully ran the %q profile.", profileName)
	return sessionLockErr // Return the error from the defer function
}

// Run handles the "regolith run" command. It runs selected profile and exports
// created resource pack and behvaiour pack to the target destination.
func Run(profileName string, debug bool) error {
	return runOrWatch(profileName, debug, false)
}

// Watch handles the "regolith watch" command. It watches the project
// directories and it runs selected profile and exports created resource pack
// and behvaiour pack to the target destination when the project changes.
func Watch(profileName string, debug bool) error {
	return runOrWatch(profileName, debug, true)
}

// Tool handles the "regolith tool" command. It runs a filter in a "tool mode".
// Tool mode modifies RP and BP file in place (using source). The config and
// properties of the tool filter are passed via commandline.
func Tool(filterName string, filterArgs []string, debug bool) error {
	InitLogging(debug)
	// Load the Config and the profile
	configJson, err := LoadConfigAsMap()
	if err != nil {
		return WrapError(err, "Could not load \"config.json\".")
	}
	config, err := ConfigFromObject(configJson)
	if err != nil {
		return WrapError(err, "Could not load \"config.json\".")
	}
	filterDefinition, ok := config.FilterDefinitions[filterName]
	if !ok {
		return WrappedErrorf(
			"Unable to find the filter on the \"filterDefinitions\" list "+
				"of the \"config.json\" file.\n"+
				"Filter name: %s", filterName)
	}
	// Get dotRegolithPath
	dotRegolithPath, err := GetDotRegolith(false, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	err = CreateDirectoryIfNotExists(dotRegolithPath)
	if err != nil {
		return WrapErrorf(err, osMkdirError, dotRegolithPath)
	}
	// Lock the session
	unlockSession, sessionLockErr := aquireSessionLock(dotRegolithPath)
	if sessionLockErr != nil {
		return WrapError(sessionLockErr, aquireSessionLockError)
	}
	defer func() {
		// WARNING: sessionLockError is not reported in case of different errors.
		// This error is minor and other errors are way more important.
		sessionLockErr = unlockSession()
	}()

	// Create the filter
	runConfiguration := map[string]interface{}{
		"filter":    filterName,
		"arguments": filterArgs,
	}
	filterRunner, err := filterDefinition.CreateFilterRunner(runConfiguration)
	if err != nil {
		return WrapErrorf(err, createFilterRunnerError, filterName)
	}
	// Create run context
	path, _ := filepath.Abs(".")
	runContext := RunContext{
		Config:              config,
		Parent:              nil,
		Profile:             "[dynamic profile]",
		DotRegolithPath:     dotRegolithPath,
		interruptionChannel: nil,
		AbsoluteLocation:    path,
	}
	// Check the filter
	err = filterRunner.Check(runContext)
	if err != nil {
		return WrapErrorf(err, filterRunnerCheckError, filterName)
	}
	// Setup tmp directory
	err = SetupTmpFiles(*config, dotRegolithPath)
	if err != nil {
		return WrapErrorf(err, setupTmpFilesError, dotRegolithPath)
	}
	// Run the filter
	Logger.Infof("Running the \"%s\" filter.", filterName)
	_, err = filterRunner.Run(runContext)
	if err != nil {
		return WrapErrorf(err, filterRunnerRunError, filterName)
	}
	// Export files to the source files
	Logger.Info("Overwriting the source files.")
	err = InplaceExportProject(config, dotRegolithPath)
	if err != nil {
		return WrapError(
			err, "Failed to overwrite the source files with generated files.")
	}
	Logger.Infof("Successfully ran the \"%s\" filter.", filterName)
	return sessionLockErr
}

// Init handles the "regolith init" command. It initializes a new Regolith
// project in the current directory.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func Init(debug bool) error {
	InitLogging(debug)
	Logger.Info("Initializing Regolith project...")

	wd, err := os.Getwd()
	if err != nil {
		return WrapError(
			err, osGetwdError)
	}
	if isEmpty, err := IsDirEmpty(wd); err != nil {
		return WrapErrorf(
			err, "Failed to check if %s is an empty directory.", wd)
	} else if !isEmpty {
		return WrappedErrorf(
			"Cannot initialze the project, because %s is not an empty "+
				"directory.\n\"regolith init\" can be used only in empty "+
				"directories.", wd)
	}
	ioutil.WriteFile(".gitignore", []byte(GitIgnore), 0644)
	// Create new default configuration
	userConfig, err := getUserConfig()
	if err != nil {
		return WrapError(err, getUserConfigError)
	}
	jsonData := Config{
		Name:   "Project name",
		Author: userConfig.Username,
		Packs: Packs{
			BehaviorFolder: "./packs/BP",
			ResourceFolder: "./packs/RP",
		},
		RegolithProject: RegolithProject{
			DataPath:          "./packs/data",
			FilterDefinitions: map[string]FilterInstaller{},
			Profiles: map[string]Profile{
				"default": {
					FilterCollection: FilterCollection{
						Filters: []FilterRunner{},
					},
					ExportTarget: ExportTarget{
						Target:   "development",
						ReadOnly: false,
					},
				},
			},
		},
	}
	jsonBytes, _ := json.MarshalIndent(jsonData, "", "")
	// Add the schema property, this is a little hacky
	rawJsonData := make(map[string]interface{}, 0)
	json.Unmarshal(jsonBytes, &rawJsonData)
	rawJsonData["$schema"] = "https://raw.githubusercontent.com/Bedrock-OSS/regolith-schemas/main/config/v1.json"
	jsonBytes, _ = json.MarshalIndent(rawJsonData, "", "\t")

	err = ioutil.WriteFile(ConfigFilePath, jsonBytes, 0644)
	if err != nil {
		return WrapErrorf(err, "Failed to write data to %q", ConfigFilePath)
	}
	var ConfigurationFolders = []string{
		"packs",
		"packs/data",
		"packs/BP",
		"packs/RP",
		filepath.Join(".regolith", "cache/venvs"),
	}
	for _, folder := range ConfigurationFolders {
		err = os.MkdirAll(folder, 0755)
		if err != nil {
			Logger.Error("Could not create folder: %s", folder, err)
		}
	}

	Logger.Info("Regolith project initialized.")
	return nil
}

// Cleans the cache folder of regolith (.regolith in normal mode or a path in
// AppData). The path to clean is determined by the dotRegolithPath parameter.
// leaveEmptyPath determines if regolith should leave an empty folder at
// dotRegolithPath
func clean(dotRegolithPath string) error {
	err := os.RemoveAll(dotRegolithPath)
	if err != nil {
		return WrapErrorf(err, "failed to remove %q folder", dotRegolithPath)
	}
	return nil
}

func CleanCurrentProject() error {
	Logger.Infof("Cleaning cache...")

	// Clean .regolith
	Logger.Infof("Cleaning \".regolith\"...")
	err := clean(".regolith")
	if err != nil {
		return WrapErrorf(
			err, "Failed to clean the cache from \".regolith\".")
	}
	// Clean cache from AppData
	Logger.Infof("Cleaning the cache in application data folder...")
	dotRegolithPath, err := getAppDataDotRegolith(true, ".")
	if err != nil {
		return WrapError(
			err, "Unable to get the path to regolith cache folder.")
	}
	Logger.Infof("Regolith cache folder is: %s", dotRegolithPath)
	err = clean(dotRegolithPath)
	if err != nil {
		return WrapErrorf(
			err, "Failed to clean the cache from %q.", dotRegolithPath)
	}
	Logger.Infof("Cache cleaned.")
	return nil
}

func CleanUserCache() error {
	Logger.Infof("Cleaning all Regolith cache files from user app data...")
	// App data enabled - use user cache dir
	userCache, err := os.UserCacheDir()
	if err != nil {
		return WrappedError(osUserCacheDirError)
	}
	regolithCacheFiles := filepath.Join(userCache, appDataCachePath)
	Logger.Infof("Regolith cache files are located in: %s", regolithCacheFiles)
	err = os.RemoveAll(regolithCacheFiles)
	if err != nil {
		return WrapErrorf(err, "failed to remove %q folder", regolithCacheFiles)
	}
	os.MkdirAll(regolithCacheFiles, 0755)
	Logger.Infof("All regolith files cached in user app data cleaned.")
	return nil
}

// Clean handles the "regolith clean" command. It cleans the cache from the
// dotRegolithPath directory.
//
// The "debug" parameter is a boolean that determines if the debug messages
// should be printed.
func Clean(debug, userCache bool) error {
	InitLogging(debug)
	if userCache {
		return CleanUserCache()
	} else {
		return CleanCurrentProject()
	}
}
