package main

// dictionaryConsumer receives the personal vocabulary used by one processing
// stage. Both consumers own the slice they receive.
type dictionaryConsumer interface {
	SetDictionary([]string)
}

// dictionaryFanout keeps startup and live settings updates on the same path.
// Adding or removing a consumer in one place therefore changes both cases.
type dictionaryFanout []dictionaryConsumer

func (f dictionaryFanout) SetDictionary(terms []string) {
	for _, consumer := range f {
		consumer.SetDictionary(terms)
	}
}
