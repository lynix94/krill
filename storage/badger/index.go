package badger

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	bolt "go.etcd.io/bbolt"
	"github.com/lynix/krill/storage"
)

// BoltIndex manages the inverted index using BoltDB
// This separates label indexing from time-series data storage
type BoltIndex struct {
	db   *bolt.DB
	mu   sync.RWMutex
	path string
}

const (
	// Bucket names in BoltDB
	postingsBucket = "postings" // labelName:labelValue -> []seriesID
	seriesBucket   = "series"   // seriesID -> labels
)

// NewBoltIndex creates a new BoltDB-backed inverted index
func NewBoltIndex(path string) (*BoltIndex, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open BoltDB index: %w", err)
	}

	// Create buckets if they don't exist
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(postingsBucket)); err != nil {
			return fmt.Errorf("failed to create postings bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(seriesBucket)); err != nil {
			return fmt.Errorf("failed to create series bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &BoltIndex{
		db:   db,
		path: path,
	}, nil
}

// AddSeries adds a new series to the index
func (idx *BoltIndex) AddSeries(seriesID uint64, labels storage.Labels) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	return idx.db.Update(func(tx *bolt.Tx) error {
		postingsBkt := tx.Bucket([]byte(postingsBucket))
		seriesBkt := tx.Bucket([]byte(seriesBucket))

		// Store series labels
		seriesKey := uint64ToBytes(seriesID)
		labelsData := serializeLabels(labels)
		if err := seriesBkt.Put(seriesKey, labelsData); err != nil {
			return fmt.Errorf("failed to store series labels: %w", err)
		}

		// Update posting lists for each label
		for _, label := range labels {
			postingKey := []byte(fmt.Sprintf("%s:%s", label.Name, label.Value))
			
			// Get existing posting list
			postingData := postingsBkt.Get(postingKey)
			var postingList []uint64
			
			if postingData != nil {
				postingList = deserializePostingList(postingData)
				
				// Check if seriesID already exists
				exists := false
				for _, id := range postingList {
					if id == seriesID {
						exists = true
						break
					}
				}
				if exists {
					continue
				}
			}
			
			// Append new seriesID
			postingList = append(postingList, seriesID)
			
			// Serialize and store
			newPostingData := serializePostingList(postingList)
			if err := postingsBkt.Put(postingKey, newPostingData); err != nil {
				return fmt.Errorf("failed to update posting list: %w", err)
			}
		}

		return nil
	})
}

// GetSeriesIDsByLabel returns all series IDs that match a label name-value pair
func (idx *BoltIndex) GetSeriesIDsByLabel(labelName, labelValue string) ([]uint64, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []uint64
	
	err := idx.db.View(func(tx *bolt.Tx) error {
		postingsBkt := tx.Bucket([]byte(postingsBucket))
		postingKey := []byte(fmt.Sprintf("%s:%s", labelName, labelValue))
		
		postingData := postingsBkt.Get(postingKey)
		if postingData == nil {
			return nil // No results
		}
		
		result = deserializePostingList(postingData)
		return nil
	})
	
	return result, err
}

// GetLabelsBySeriesID returns the labels for a given series ID
func (idx *BoltIndex) GetLabelsBySeriesID(seriesID uint64) (storage.Labels, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var labels storage.Labels
	
	err := idx.db.View(func(tx *bolt.Tx) error {
		seriesBkt := tx.Bucket([]byte(seriesBucket))
		seriesKey := uint64ToBytes(seriesID)
		
		labelsData := seriesBkt.Get(seriesKey)
		if labelsData == nil {
			return fmt.Errorf("series not found: %d", seriesID)
		}
		
		var err error
		labels, err = deserializeLabels(labelsData)
		return err
	})
	
	return labels, err
}

// GetAllSeries returns all series IDs and their labels
func (idx *BoltIndex) GetAllSeries() (map[uint64]storage.Labels, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	result := make(map[uint64]storage.Labels)
	
	err := idx.db.View(func(tx *bolt.Tx) error {
		seriesBkt := tx.Bucket([]byte(seriesBucket))
		
		return seriesBkt.ForEach(func(k, v []byte) error {
			seriesID := bytesToUint64(k)
			labels, err := deserializeLabels(v)
			if err != nil {
				log.Printf("[INDEX] Warning: skipping corrupted series %d: %v", seriesID, err)
				return nil // Continue iteration
			}
			result[seriesID] = labels
			return nil
		})
	})
	
	return result, err
}

// GetAllPostings returns all posting lists (for debugging/migration)
func (idx *BoltIndex) GetAllPostings() (map[string][]uint64, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	result := make(map[string][]uint64)
	
	err := idx.db.View(func(tx *bolt.Tx) error {
		postingsBkt := tx.Bucket([]byte(postingsBucket))
		
		return postingsBkt.ForEach(func(k, v []byte) error {
			key := string(k)
			postingList := deserializePostingList(v)
			result[key] = postingList
			return nil
		})
	})
	
	return result, err
}

// Stats returns index statistics
func (idx *BoltIndex) Stats() (seriesCount int, postingListCount int, err error) {
	err = idx.db.View(func(tx *bolt.Tx) error {
		seriesBkt := tx.Bucket([]byte(seriesBucket))
		postingsBkt := tx.Bucket([]byte(postingsBucket))
		
		seriesCount = seriesBkt.Stats().KeyN
		postingListCount = postingsBkt.Stats().KeyN
		
		return nil
	})
	
	return
}

// Close closes the BoltDB index
func (idx *BoltIndex) Close() error {
	return idx.db.Close()
}

// Helper functions for serialization

func uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

func bytesToUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

func serializePostingList(ids []uint64) []byte {
	buf := make([]byte, len(ids)*8)
	for i, id := range ids {
		binary.BigEndian.PutUint64(buf[i*8:], id)
	}
	return buf
}

func deserializePostingList(data []byte) []uint64 {
	if len(data)%8 != 0 {
		return nil
	}
	ids := make([]uint64, len(data)/8)
	for i := 0; i < len(ids); i++ {
		ids[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	return ids
}
