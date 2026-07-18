package transport

import "os"

type verifiedScriptLaunch struct {
	Path       string
	ExtraFiles []*os.File
	cleanup    func()
}

func (p *verifiedScriptLaunch) close() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
		p.cleanup = nil
	}
}
