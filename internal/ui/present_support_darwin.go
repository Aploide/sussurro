package ui

// overlayPresents follows the overlay type, so implementing Presenter on it
// is all that is needed to turn text rendering on.
var _, overlayPresents = Overlay((*darwinOverlay)(nil)).(Presenter)
