-- +goose Up
ALTER TABLE customers
 ADD COLUMN hair_texture text CHECK (hair_texture IS NULL OR char_length(hair_texture) <= 40),
 ADD COLUMN preferences text CHECK (preferences IS NULL OR char_length(preferences) <= 200),
 ADD COLUMN dislikes text CHECK (dislikes IS NULL OR char_length(dislikes) <= 200);

-- +goose Down
ALTER TABLE customers
 DROP COLUMN dislikes,
 DROP COLUMN preferences,
 DROP COLUMN hair_texture;
