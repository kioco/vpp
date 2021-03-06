// Copyright (c) 2018 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package linuxcalls

import (
	"github.com/go-errors/errors"
	"github.com/ligato/vpp-agent/plugins/linux/model/interfaces"
	"github.com/ligato/vpp-agent/plugins/linux/nsplugin"
	"github.com/vishvananda/netlink"
)

// LinuxInterfaceDetails is the wrapper structure for the linux interface northbound API structure.
type LinuxInterfaceDetails struct {
	Interface *interfaces.LinuxInterfaces_Interface `json:"linux_interface"`
	Meta      *LinuxInterfaceMeta                   `json:"linux_interface_meta"`
}

// LinuxInterfaceMeta is combination of proto-modelled Interface data and linux provided metadata
type LinuxInterfaceMeta struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Alias       string `json:"alias"`
	OperState   string `json:"oper_state"`
	Flags       string `json:"flags"`
	MacAddr     string `json:"mac_addr"`
	Mtu         int    `json:"mtu"`
	Type        string `json:"type"`
	NetNsID     int    `json:"net_ns_id"`
	NumTxQueues int    `json:"num_tx_queues"`
	TxQueueLen  int    `json:"tx_queue_len"`
	NumRxQueues int    `json:"num_rx_queues"`
}

// DumpInterfaces is an implementation of linux interface handler
func (h *NetLinkHandler) DumpInterfaces() ([]*LinuxInterfaceDetails, error) {
	var ifs []*LinuxInterfaceDetails

	ctx := nsplugin.NewNamespaceMgmtCtx()

	// Dump all interfaces from the default namespace
	dNsLinks, err := h.GetLinkList()
	if err != nil {
		return nil, errors.Errorf("failed to dump linux interfaces from default namespace: %v", err)
	}
	for _, dNsLink := range dNsLinks {
		ifDetails := &LinuxInterfaceDetails{}
		if dNsLink == nil || dNsLink.Attrs() == nil {
			h.log.Warnf("Unable to get link data for interface %s", dNsLink)
			continue
		}
		ifName := dNsLink.Attrs().Name
		link, linkAddresses, err := h.dumpInterfaceData(ifName, &nsplugin.Namespace{}, ctx)
		if err != nil {
			// Do not return error
			h.log.Errorf("failed to get interface %s data: %v", ifName, err)
			continue
		}

		// If the interface exists in mapping, copy the modelled data
		if _, meta, found := h.ifIndexes.LookupIdx(ifName); found {
			ifDetails.Interface = meta.Data
		}

		h.transformAndStore(link, linkAddresses, ifDetails)
		ifs = append(ifs, ifDetails)
	}

	// Add all known interfaces in non-default namespace
	for _, ifName := range h.ifIndexes.GetMapping().ListNames() {
		ifDetails := &LinuxInterfaceDetails{}
		_, meta, found := h.ifIndexes.LookupIdx(ifName)
		if !found {
			h.log.Warnf("Expected interface %s not found in the mapping", ifName)
			continue
		}
		if meta == nil || meta.Data == nil {
			h.log.Warnf("Expected interface %s metadata are missing", ifName)
			continue
		}
		// Skip interfaces in default NS (already processed)
		if meta.Data.Namespace == nil {
			continue
		}

		// Copy base configuration from mapping metadata. Linux specific fields are stored in LinuxInterfaceMeta.
		ifDetails.Interface = meta.Data

		// Check the interface namespace
		link, linkAddresses, err := h.dumpInterfaceData(ifName, h.nsHandler.IfNsToGeneric(meta.Data.Namespace), ctx)
		if err != nil {
			// Do not return error, read what is possible
			h.log.Errorf("failed to get interface %s data: %v", ifName, err)
			continue
		}

		if link == nil || link.Attrs() == nil {
			h.log.Warnf("Unable to get link data for interface %s", ifName)
			continue
		}

		h.transformAndStore(link, linkAddresses, ifDetails)
		ifs = append(ifs, ifDetails)
	}

	return ifs, nil
}

// LinuxInterfaceStatistics returns linux interface name/index with statistics data
type LinuxInterfaceStatistics struct {
	Name       string
	Index      int
	Statistics *netlink.LinkStatistics
}

// DumpInterfaceStatistics is an implementation of linux interface handler
func (h *NetLinkHandler) DumpInterfaceStatistics() ([]*LinuxInterfaceStatistics, error) {
	var ifs []*LinuxInterfaceStatistics

	ctx := nsplugin.NewNamespaceMgmtCtx()

	// Iterate over all known interfaces
	for _, ifName := range h.ifIndexes.GetMapping().ListNames() {
		_, meta, found := h.ifIndexes.LookupIdx(ifName)
		if !found {
			h.log.Warnf("Expected interface %s not found in the mapping", ifName)
			continue
		}
		if meta == nil || meta.Data == nil {
			h.log.Warnf("Expected interface %s metadata are missing", ifName)
			continue
		}

		// Check the interface namespace
		link, _, err := h.dumpInterfaceData(ifName, h.nsHandler.IfNsToGeneric(meta.Data.Namespace), ctx)
		if err != nil {
			// Do not return error, read what is possible
			h.log.Errorf("failed to get interface %s data: %v", ifName, err)
			continue
		}

		if link == nil || link.Attrs() == nil {
			h.log.Warnf("Unable to get link data for interface %s", ifName)
			continue
		}

		linkAttrs := link.Attrs()

		// Fill data
		linuxStats := &LinuxInterfaceStatistics{
			Name:       linkAttrs.Name,
			Index:      linkAttrs.Index,
			Statistics: linkAttrs.Statistics,
		}

		ifs = append(ifs, linuxStats)
	}

	return ifs, nil
}

// Reads interface data and ip addresses from provided namespace
func (h *NetLinkHandler) dumpInterfaceData(ifName string, ns *nsplugin.Namespace, ctx *nsplugin.NamespaceMgmtCtx) (netlink.Link, []netlink.Addr, error) {
	revert, err := h.nsHandler.SwitchNamespace(ns, ctx)
	defer revert()

	if err != nil {
		return nil, nil, errors.Errorf("failed to switch to namespace %s: %v", ns.Name, err)
	}
	link, err := h.GetLinkByName(ifName)
	if err != nil {
		return nil, nil, errors.Errorf("failed to get interface %s from namespace %s: %v", ifName, ns.Name, err)
	}
	linkAddrs, err := h.GetAddressList(ifName)
	if err != nil {
		return nil, nil, errors.Errorf("failed to get interface %s addresses: %v", ifName, err)
	}

	return link, linkAddrs, nil
}

// Transform link type interface to the interface details and store IP addresses to the proto modelled data
func (h *NetLinkHandler) transformAndStore(link netlink.Link, addr []netlink.Addr, ifDetails *LinuxInterfaceDetails) {
	// Set IP addresses to model-based data
	var ipAddrs []string
	for _, linkAddr := range addr {
		ipAddrs = append(ipAddrs, linkAddr.String())
	}
	if ifDetails.Interface == nil {
		ifDetails.Interface = &interfaces.LinuxInterfaces_Interface{
			IpAddresses: ipAddrs,
		}
	} else {
		ifDetails.Interface.IpAddresses = ipAddrs
	}

	// Metadata fields
	linkAttrs := link.Attrs()
	linuxMeta := &LinuxInterfaceMeta{
		Index:       linkAttrs.Index,
		Name:        linkAttrs.Name,
		Alias:       linkAttrs.Alias,
		OperState:   linkAttrs.OperState.String(),
		Flags:       linkAttrs.Flags.String(),
		Mtu:         linkAttrs.MTU,
		Type:        linkAttrs.EncapType,
		NetNsID:     linkAttrs.NetNsID,
		NumTxQueues: linkAttrs.NumTxQueues,
		TxQueueLen:  linkAttrs.TxQLen,
		NumRxQueues: linkAttrs.NumRxQueues,
	}
	if linkAttrs.HardwareAddr != nil {
		linuxMeta.MacAddr = linkAttrs.HardwareAddr.String()
	}
	ifDetails.Meta = linuxMeta
}
