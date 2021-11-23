package probe

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path"

	"k8s.io/klog"

	"github.com/openebs/node-disk-manager/blockdevice"
	"github.com/openebs/node-disk-manager/cmd/ndm_daemonset/controller"
	libudevwrapper "github.com/openebs/node-disk-manager/pkg/udev"
	"github.com/openebs/node-disk-manager/pkg/util"
)

const (
	megaRaidConfigKey     = "mega-raid-probe"
	megaRaidProbePriority = 2
	megaCliPath           = "/opt/MegaRAID/storcli/storcli64"
)

var (
	megaRaidProbeName  = "mega raid probe"
	megaRaidProbeState = defaultEnabled
)

var megaRaidProbeRegister = func() {
	ctrl := <-controller.ControllerBroadcastChannel
	if ctrl == nil {
		klog.Error("unable to configure", megaRaidProbeName)
		return
	}
	if ctrl.NDMConfig != nil {
		for _, probeConfig := range ctrl.NDMConfig.ProbeConfigs {
			if probeConfig.Key == megaRaidConfigKey {
				megaRaidProbeName = probeConfig.Name
				megaRaidProbeState = util.CheckTruthy(probeConfig.State)
				break
			}
		}
	}

	newRegistryProbe := &registerProbe{
		priority:   megaRaidProbePriority,
		name:       megaRaidProbeName,
		state:      megaRaidProbeState,
		pi:         newMegaRaidProbe(),
		controller: ctrl,
	}
	newRegistryProbe.register()
}

type megaRaidProbe struct {
}

func newMegaRaidProbe() *megaRaidProbe {
	return &megaRaidProbe{}
}

func (m *megaRaidProbe) Start() {
}

func (m *megaRaidProbe) FillBlockDeviceDetails(blockDevice *blockdevice.BlockDevice) {

	byIds := m.getByIds(blockDevice)

	if len(byIds) == 0 {
		klog.Errorf("%s byids not found", blockDevice.Identifier.DevPath)
		return
	}

	vds, err := m.getVdInfos()
	if err != nil {
		klog.Errorf("%s err: %v", blockDevice.Identifier.DevPath, err)
		return
	}

	for _, vdItem := range vds {
		for _, byId := range byIds {
			if path.Base(byId) == vdItem.WWN {
				klog.Infof("set %s driver type %s", blockDevice.Identifier.DevPath, vdItem.DriverType)
				blockDevice.DeviceAttributes.DriveType = vdItem.DriverType
			}
		}
	}

}

func (m *megaRaidProbe) getByIds(blockDevice *blockdevice.BlockDevice) (byIds []string) {
	for _, item := range blockDevice.DevLinks {
		if item.Kind == libudevwrapper.BY_ID_LINK {
			return item.Links
		}
	}
	return
}

func (m *megaRaidProbe) getVdInfos() (vdInfos []VdInfo, err error) {
	arg := []string{"/call/vall", "show", "all", "J"}
	cmd := exec.Command(megaCliPath, arg...)
	buf, err := cmd.Output()
	if err != nil {
		return
	}
	output := MegaOutput{}
	err = json.Unmarshal(buf, &output)
	if err != nil {
		return
	}

	for _, ctl := range output.Controllers {
		ctlStatus := ctl.CommandStatus.Status
		if ctlStatus != "Success" {
			err = fmt.Errorf(ctl.CommandStatus.Description)
			return
		}
		maxNum := 100
		for i := 0; i <= maxNum; i++ {
			vdKey := fmt.Sprintf("VD%d Properties", i)
			pdKey := fmt.Sprintf("PDs for VD %d", i)
			vdArray, ok := ctl.ResponseStatus[vdKey]
			if !ok {
				break
			}
			pdArray, ok := ctl.ResponseStatus[pdKey]
			if !ok {
				break
			}
			pds, err := m.convertPd(pdArray)
			if err != nil {
				return vdInfos, err
			}
			vds, err := m.convertVd(vdArray)
			if err != nil {
				return vdInfos, err
			}
			vdItem := VdInfo{
				WWN:        fmt.Sprintf("wwn-0x%s", vds.SCSI),
				DriverType: pds[0].Med,
			}
			vdInfos = append(vdInfos, vdItem)
		}
	}
	return
}

func (m *megaRaidProbe) convertPd(data interface{}) (pds []PdObj, err error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	err = json.Unmarshal(bytes, &pds)
	if err != nil {
		return
	}
	return
}

func (m *megaRaidProbe) convertVd(data interface{}) (vds VdObj, err error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	err = json.Unmarshal(bytes, &vds)
	if err != nil {
		return
	}
	return
}

type VdInfo struct {
	WWN        string
	DriverType string
}

type MegaOutput struct {
	Controllers []MegaCtl `json:"Controllers"`
}

type MegaCtl struct {
	CommandStatus  CommandStatus          `json:"Command Status"`
	ResponseStatus map[string]interface{} `json:"Response Data"`
}

type PdObj struct {
	// "Med" : "HDD"
	Med string `json:"Med"`
}

type VdObj struct {
	// "SCSI NAA Id" : "6a416e7a06f9600027d34e94a257db13"
	SCSI string `json:"SCSI NAA Id"`
}

type CommandStatus struct {
	Controller  int    `json:"Controller"`
	Status      string `json:"Status"`
	Description string `json:"Description"`
}
