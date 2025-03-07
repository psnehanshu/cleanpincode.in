-- name: GetByPincode :many
SELECT * FROM pincodes
WHERE Pincode = $1;

-- name: GetPincodeMedia :many
SELECT vp.* FROM pincodes p
INNER JOIN votes v ON v.pincode = p.Pincode
INNER JOIN vote_pics vp ON vp.vote_id = v.id
WHERE p.Pincode = $1
ORDER BY vp.created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetPincodes :many
SELECT DISTINCT Pincode FROM pincodes
ORDER BY Pincode
LIMIT $1 OFFSET $2;

-- name: GetStates :many
SELECT DISTINCT StateName FROM pincodes
ORDER BY StateName;

-- name: GetDistricts :many
SELECT DISTINCT District FROM pincodes
WHERE StateName = $1
ORDER BY District;

-- name: GetPincodeByDistrict :many
SELECT DISTINCT Pincode FROM pincodes
WHERE District = $1 AND StateName = $2
ORDER BY Pincode;

-- name: GetPincodeScoreboard :one
SELECT * FROM scoreboard
WHERE pincode = $1;

-- name: GetVoteCommentsByPincode :many
SELECT v.*, u.name voter_name, u.pic voter_pic FROM votes v
LEFT JOIN users u on u.id = v.voter_id
WHERE v.pincode = $1 AND v.comment IS NOT NULL AND TRIM(v.comment) <> ''
ORDER BY v.created_at DESC LIMIT $2 OFFSET $3;

-- name: MostUpvoted :many
select
	pincode,
	(upvotes-downvotes) total
from
	scoreboard
order by
	(upvotes-downvotes) desc
limit $1 offset $2;

-- name: MostDownvoted :many
select
	pincode,
	(upvotes-downvotes) total
from
	scoreboard
order by
	(upvotes-downvotes) asc
limit $1 offset $2;

-- name: CreateUser :one
INSERT INTO users (name, email, google_id, pic) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetUserByGoogleID :one
SELECT * FROM users WHERE google_id = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetVote :one
SELECT * FROM votes WHERE pincode = $1 AND voter_id = $2;

-- name: CreateVote :one
INSERT INTO votes (type, pincode, voter_id, comment) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UpdateExistingVote :exec
UPDATE votes SET type = $1, comment = $3 WHERE id = $2;

-- name: InsertVoteFiles :exec
INSERT INTO vote_pics (link, vote_id) VALUES (  
  unnest($1::text[]),  
  unnest($2::UUID[])  
);

-- name: GetMediaInfo :one
SELECT * FROM vote_pics WHERE id = $1;

-- name: Search :many
SELECT Pincode, District, StateName FROM Pincodes
WHERE District ILIKE $1 OR pincode::TEXT ILIKE $1 OR StateName ILIKE $1
GROUP BY Pincode, District, StateName
ORDER BY Pincode
LIMIT $2 OFFSET $3;
