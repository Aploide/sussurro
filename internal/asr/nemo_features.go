package asr

import (
	"fmt"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	nemoSampleRate = 16000
	nemoFFT        = 512
	nemoWinLength  = 400
	nemoHopLength  = 160
	nemoPreemph    = 0.97
	nemoLogGuard   = 1.0 / (1 << 24)
)

func nemo128Features(samples []float32) ([]float32, int, error) {
	if len(samples) < nemoWinLength {
		return nil, 0, fmt.Errorf("audio too short for features")
	}

	wave := make([]float64, len(samples))
	for i := range samples {
		wave[i] = float64(samples[i])
	}
	for i := len(wave) - 1; i >= 1; i-- {
		wave[i] = wave[i] - nemoPreemph*wave[i-1]
	}

	frames := 1 + (len(wave)-nemoWinLength)/nemoHopLength
	if frames <= 0 {
		return nil, 0, fmt.Errorf("no frames generated")
	}

	window := make([]float64, nemoWinLength)
	for i := 0; i < nemoWinLength; i++ {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(nemoWinLength-1))
	}

	fft := fourier.NewFFT(nemoFFT)
	powerBins := nemoFFT/2 + 1
	melFB := buildMelFilterBank(powerBins, 128, nemoSampleRate, 0, nemoSampleRate/2)

	feat := make([]float32, 128*frames)
	signal := make([]float64, nemoFFT)
	power := make([]float64, powerBins)
	mel := make([]float64, 128)

	for f := 0; f < frames; f++ {
		start := f * nemoHopLength
		for i := 0; i < nemoFFT; i++ {
			signal[i] = 0
		}
		for i := 0; i < nemoWinLength; i++ {
			signal[i] = wave[start+i] * window[i]
		}

		spec := fft.Coefficients(nil, signal)
		for i := 0; i < powerBins; i++ {
			re := real(spec[i])
			im := imag(spec[i])
			power[i] = re*re + im*im
		}

		for m := 0; m < 128; m++ {
			sum := 0.0
			row := melFB[m]
			for i := 0; i < powerBins; i++ {
				sum += power[i] * row[i]
			}
			mel[m] = math.Log(sum + nemoLogGuard)
		}

		mean := 0.0
		for m := 0; m < 128; m++ {
			mean += mel[m]
		}
		mean /= 128
		variance := 0.0
		for m := 0; m < 128; m++ {
			d := mel[m] - mean
			variance += d * d
		}
		std := math.Sqrt(variance/127.0 + 1e-5)
		for m := 0; m < 128; m++ {
			feat[m*frames+f] = float32((mel[m] - mean) / std)
		}
	}

	return feat, frames, nil
}

func hzToMel(hz float64) float64 {
	return 2595.0 * math.Log10(1.0+hz/700.0)
}

func melToHz(mel float64) float64 {
	return 700.0 * (math.Pow(10, mel/2595.0) - 1.0)
}

func buildMelFilterBank(nFFT, nMels, sampleRate int, fMin, fMax float64) [][]float64 {
	fullFFT := (nFFT - 1) * 2
	melMin := hzToMel(fMin)
	melMax := hzToMel(fMax)
	melPoints := make([]float64, nMels+2)
	for i := 0; i < nMels+2; i++ {
		melPoints[i] = melMin + float64(i)*(melMax-melMin)/float64(nMels+1)
	}

	hzPoints := make([]float64, len(melPoints))
	for i, m := range melPoints {
		hzPoints[i] = melToHz(m)
	}

	bins := make([]int, len(hzPoints))
	for i, hz := range hzPoints {
		b := int(math.Floor((float64(fullFFT)+1.0)*hz/float64(sampleRate) + 0.5))
		if b < 0 {
			b = 0
		}
		if b > nFFT-1 {
			b = nFFT - 1
		}
		bins[i] = b
	}

	fb := make([][]float64, nMels)
	for m := 1; m <= nMels; m++ {
		fb[m-1] = make([]float64, nFFT)
		left := bins[m-1]
		center := bins[m]
		right := bins[m+1]
		if center <= left {
			center = left + 1
		}
		if right <= center {
			right = center + 1
		}
		for k := left; k < center && k < nFFT; k++ {
			fb[m-1][k] = float64(k-left) / float64(center-left)
		}
		for k := center; k < right && k < nFFT; k++ {
			fb[m-1][k] = float64(right-k) / float64(right-center)
		}
	}
	return fb
}
