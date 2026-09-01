-- +goose Up
INSERT INTO categories (slug, name, sort_order) VALUES
  ('music', 'Music', 1),
  ('sports', 'Sports & Fitness', 2),
  ('arts', 'Arts & Theatre', 3),
  ('food-drink', 'Food & Drink', 4),
  ('tech', 'Tech & Innovation', 5),
  ('community', 'Community', 6),
  ('nightlife', 'Nightlife', 7),
  ('family', 'Family & Kids', 8)
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DELETE FROM categories WHERE slug IN ('music','sports','arts','food-drink','tech','community','nightlife','family');