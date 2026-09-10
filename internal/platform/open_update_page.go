package platform

import "TcNo-Acc-Switcher/internal/updatecheck"

// OpenUpdateDownloadPage opens the latest GitHub release page in the default browser.
func (p *PlatformService) OpenUpdateDownloadPage() error {
	return OpenURL(updatecheck.ReleasePageURL)
}
