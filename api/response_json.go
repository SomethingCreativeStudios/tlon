package api

// The strict generator represents some JSON response bodies as new defined
// types. Forward their JSON encoding to the model type so arbitrary extension
// properties preserved by the model are not dropped.
func (response GetCatalog200JSONResponse) MarshalJSON() ([]byte, error) {
	return Catalog(response).MarshalJSON()
}
func (response GetFacets200ApplicationFacetsPlusJSONResponse) MarshalJSON() ([]byte, error) {
	return Facets(response).MarshalJSON()
}
