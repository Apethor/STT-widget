package desktop

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Filled paths stay legible at the same small sizes as Fyne's action icons.
var (
	inputDevicesIcon = theme.NewThemedResource(fyne.NewStaticResource("input-devices.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M8 2a4 4 0 0 1 4 4v6a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4zm0 2a2 2 0 0 0-2 2v6a2 2 0 1 0 4 0V6a2 2 0 0 0-2-2zM1 10h2v2a5 5 0 0 0 10 0v-2h2v2a7 7 0 0 1-6 6.93V21h3v2H4v-2h3v-2.07A7 7 0 0 1 1 12zm17-6h4v2h-4zm-1 5h6v2h-6zm1 5h4v2h-4z"/></svg>`,
	)))
	teamInputIcon = theme.NewThemedResource(fyne.NewStaticResource("team-input.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M8 2a4 4 0 0 1 4 4v6a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4zm0 2a2 2 0 0 0-2 2v6a2 2 0 1 0 4 0V6a2 2 0 0 0-2-2zM1 10h2v2a5 5 0 0 0 10 0v-2h2v2a7 7 0 0 1-6 6.93V21h3v2H4v-2h3v-2.07A7 7 0 0 1 1 12zm18 5h2v3h3v2h-3v3h-2v-3h-3v-2h3z"/></svg>`,
	)))
	streamIcon = theme.NewThemedResource(fyne.NewStaticResource("stream.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="m4.2 3.2 1.4 1.4a10 10 0 0 0 0 14.8l-1.4 1.4a12 12 0 0 1 0-17.6zm15.6 0a12 12 0 0 1 0 17.6l-1.4-1.4a10 10 0 0 0 0-14.8zm-13 3 1.4 1.4a6 6 0 0 0 0 8.8l-1.4 1.4a8 8 0 0 1 0-11.6zm10.4 0a8 8 0 0 1 0 11.6l-1.4-1.4a6 6 0 0 0 0-8.8zM12 8l5 4-5 4z"/></svg>`,
	)))
	textAnnotationIcon = theme.NewThemedResource(fyne.NewStaticResource("text-annotation.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M3 3h18v5h-2V6h-5v13h3v2H7v-2h3V6H5v2H3z"/></svg>`,
	)))
	cameraIcon = theme.NewThemedResource(fyne.NewStaticResource("camera.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M8 3h8l2 3h4v15H2V6h4zm1 2L7 8H4v11h16V8h-4l-2-3zm3 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8zm0 2a2 2 0 1 1 0 4 2 2 0 0 1 0-4z"/></svg>`,
	)))
	videoIcon = theme.NewThemedResource(fyne.NewStaticResource("video.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M3 5h12a2 2 0 0 1 2 2v2l5-3v12l-5-3v2a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2zm0 2v10h12V7z"/></svg>`,
	)))
	soundboardIcon = theme.NewThemedResource(fyne.NewStaticResource("soundboard.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M3 3h8v8H3zm2 2v4h4V5zm8-2h8v8h-8zm2 2v4h4V5zM3 13h8v8H3zm2 2v4h4v-4zm10-2h2v4h4v2h-4v4h-2v-4h-4v-2h4z"/></svg>`,
	)))
	brushIcon = theme.NewThemedResource(fyne.NewStaticResource("brush.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="m17 2 5 5-11 11-5-5zM4 14l6 6c-2 3-5 3-9 3 2-2 0-7 3-9z"/></svg>`,
	)))
	eraserIcon = theme.NewThemedResource(fyne.NewStaticResource("eraser.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="m14 2 9 9-10 10H6l-5-5zm0 3-6 6 6 6 6-6zM6 13l-2 3 3 3h5l1-1z"/></svg>`,
	)))
	pinIcon = theme.NewThemedResource(fyne.NewStaticResource("pin.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M8 2h8v2h-1v6l3 3v2h-5v5l-1 3-1-3v-5H6v-2l3-3V4H8z"/></svg>`,
	)))
	mergeIcon = theme.NewThemedResource(fyne.NewStaticResource("merge.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M11 8V5H8l4-4 4 4h-3v3c0 4 2 6 6 8v6h-2v-5c-2-1-4-3-5-5-1 2-3 4-5 5v5H5v-6c4-2 6-4 6-8z"/></svg>`,
	)))
	closeTabsIcon = theme.NewThemedResource(fyne.NewStaticResource("close-tabs.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M2 2h15v2H4v13H2z"/><path fill="#000" fill-rule="evenodd" d="M6 6h16v16H6zm2 2v12h12V8z"/><path fill="#000" d="m10 10 4 4 4-4 1.4 1.4-4 4 4 4-1.4 1.4-4-4-4 4-1.4-1.4 4-4-4-4z"/></svg>`,
	)))
	webSearchIcon = theme.NewThemedResource(fyne.NewStaticResource("web-search.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm-7.75 8h3.83c.14-2.22.58-4.14 1.25-5.54A8.02 8.02 0 0 0 4.25 10zm0 4a8.02 8.02 0 0 0 5.08 5.54C8.66 18.14 8.22 16.22 8.08 14zm15.5-4a8.02 8.02 0 0 0-5.08-5.54c.67 1.4 1.11 3.32 1.25 5.54zm0 4h-3.83c-.14 2.22-.58 4.14-1.25 5.54A8.02 8.02 0 0 0 19.75 14zM10.09 10h3.82C13.66 6.25 12.58 4 12 4s-1.66 2.25-1.91 6zm0 4c.25 3.75 1.33 6 1.91 6s1.66-2.25 1.91-6zM4 12h16v-1H4z"/></svg>`,
	)))
	processTextIcon = theme.NewThemedResource(fyne.NewStaticResource("process-text.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M3 4h9v2H3zm0 6h8v2H3zm0 6h12v2H3zm0 4h16v2H3zm14-18 1.5 4.5L23 8l-4.5 1.5L17 14l-1.5-4.5L11 8l4.5-1.5z"/></svg>`,
	)))
	cloudIcon = theme.NewThemedResource(fyne.NewStaticResource("cloud.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M7 19a5 5 0 0 1-1-9.9 6 6 0 0 1 11.6-.6A5.3 5.3 0 0 1 18 19zm0-2h11a3.3 3.3 0 0 0-.3-6.6l-1.6-.2-.4-1.5A4 4 0 0 0 8 10l-.1 1-1 .1A3 3 0 0 0 7 17z"/></svg>`,
	)))
	groupsIcon = theme.NewThemedResource(fyne.NewStaticResource("groups.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000" d="M2 3h7v5H2zm9 0h11v5H11zM2 10h7v5H2zm9 0h5v5h-5zM2 17h7v5H2zm9 0h5v5h-5zm8-6h2v4h3v2h-3v4h-2v-4h-3v-2h3z"/></svg>`,
	)))
)
