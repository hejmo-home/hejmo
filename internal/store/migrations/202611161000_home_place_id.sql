-- The home location is picked from the list of places on the box (#86, ADR 0022).
-- home_place_id is that place's GeoNames id, so the box can rebuild the region
-- line in the page language. NULL when the coordinates were set through the API.
ALTER TABLE households ADD COLUMN home_place_id INTEGER;
