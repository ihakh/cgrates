/*
Real-time Online/Offline Charging System (OCS) for Telecom & ISP environments
Copyright (C) ITsysCOM GmbH

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package engine

import (
	"fmt"
	"strings"

	"github.com/cgrates/cgrates/config"
	"github.com/cgrates/cgrates/guardian"
	"github.com/cgrates/cgrates/utils"
)

// newFilterIndex will get the index from DataManager if is not found it will create it
func newFilterIndex(dm *DataManager, idxItmType, tnt, ctx, itemID string, filterIDs []string) (indexes map[string]utils.StringSet, err error) {
	tntCtx := tnt
	if ctx != utils.EmptyString {
		tntCtx = utils.ConcatenatedKey(tnt, ctx)
	}
	indexes = make(map[string]utils.StringSet)
	if len(filterIDs) == 0 { // in case of None
		idxKey := utils.ConcatenatedKey(utils.META_NONE, utils.META_ANY, utils.META_ANY)
		var rcvIndx map[string]utils.StringSet
		if rcvIndx, err = dm.GetIndexes(idxItmType, tntCtx,
			idxKey,
			true, true); err != nil {
			if err != utils.ErrNotFound {
				return
			}
			err = nil
			indexes[idxKey] = make(utils.StringSet) // create an empty index if is not found in DB in case we add them later
			return
		}
		for idxKey, idx := range rcvIndx { // parse the received indexes
			indexes[idxKey] = idx
		}
		return
	}
	for _, fltrID := range filterIDs {
		var fltr *Filter
		if fltr, err = dm.GetFilter(tnt, fltrID,
			true, false, utils.NonTransactional); err != nil {
			if err == utils.ErrNotFound {
				err = fmt.Errorf("broken reference to filter: %+v for itemType: %+v and ID: %+v",
					fltrID, idxItmType, itemID)
			}
			return
		}
		for _, flt := range fltr.Rules {
			if !utils.SliceHasMember([]string{utils.MetaPrefix, utils.MetaString}, flt.Type) {
				continue
			}

			for _, fldVal := range flt.Values {
				idxKey := utils.ConcatenatedKey(flt.Type, flt.Element, fldVal)
				var rcvIndx map[string]utils.StringSet
				if rcvIndx, err = dm.GetIndexes(idxItmType, tntCtx,
					idxKey, true, false); err != nil {
					if err != utils.ErrNotFound {
						return
					}
					err = nil
					indexes[idxKey] = make(utils.StringSet) // create an empty index if is not found in DB in case we add them later
					continue
				}
				for idxKey, idx := range rcvIndx { // parse the received indexes
					indexes[idxKey] = idx
				}
			}
		}
	}
	return
}

// addItemToFilterIndex will add the itemID to the existing/created index and set it in the DataDB
func addItemToFilterIndex(dm *DataManager, idxItmType, tnt, ctx, itemID string, filterIDs []string) (err error) {
	var indexes map[string]utils.StringSet
	if indexes, err = newFilterIndex(dm, idxItmType, tnt, ctx, itemID, filterIDs); err != nil {
		return
	}
	if len(indexes) == 0 { // in case we have a profile with only non indexable filters(e.g. only *gt)
		return
	}
	tntCtx := tnt
	if ctx != utils.EmptyString {
		tntCtx = utils.ConcatenatedKey(tnt, ctx)
	}
	refID := guardian.Guardian.GuardIDs("",
		config.CgrConfig().GeneralCfg().LockingTimeout, idxItmType+tntCtx)
	defer guardian.Guardian.UnguardIDs(refID)

	for _, index := range indexes {
		index.Add(itemID)
	}

	for indxKey := range indexes {
		if err = Cache.Remove(idxItmType, utils.ConcatenatedKey(tntCtx, indxKey), true, utils.NonTransactional); err != nil {
			return
		}
	}

	return dm.SetIndexes(idxItmType, tntCtx, indexes, true, utils.NonTransactional)
}

// addItemToFilterIndex will remove the itemID from the existing/created index and set it in the DataDB
func removeItemFromFilterIndex(dm *DataManager, idxItmType, tnt, ctx, itemID string, filterIDs []string) (err error) {
	var indexes map[string]utils.StringSet
	if indexes, err = newFilterIndex(dm, idxItmType, tnt, ctx, itemID, filterIDs); err != nil {
		return
	}
	if len(indexes) == 0 { // in case we have a profile with only non indexable filters(e.g. only *gt)
		return
	}

	tntCtx := tnt
	if ctx != utils.EmptyString {
		tntCtx = utils.ConcatenatedKey(tnt, ctx)
	}
	refID := guardian.Guardian.GuardIDs("",
		config.CgrConfig().GeneralCfg().LockingTimeout, idxItmType+tntCtx)
	defer guardian.Guardian.UnguardIDs(refID)

	for idxKey, index := range indexes {
		index.Remove(itemID)
		if index.Size() == 0 { // empty index set it with nil for cache
			indexes[idxKey] = nil // this will not be set in DB(handled by driver)
		}
	}

	for indxKey := range indexes {
		if err = Cache.Remove(idxItmType, utils.ConcatenatedKey(tntCtx, indxKey), true, utils.NonTransactional); err != nil {
			return
		}
	}
	return dm.SetIndexes(idxItmType, tntCtx, indexes, true, utils.NonTransactional)
}

// updatedIndexes will compare the old filtersIDs with the new ones and only uptdate the filters indexes that are added/removed
func updatedIndexes(dm *DataManager, idxItmType, tnt, ctx, itemID string, oldFilterIds *[]string, newFilterIDs []string) (err error) {
	if oldFilterIds == nil { // nothing to remove so just create the new indexes
		if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, newFilterIDs); err != nil {
			return
		}
		return addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, newFilterIDs)
	}
	if len(*oldFilterIds) == 0 && len(newFilterIDs) == 0 { // nothing to update
		return
	}

	// check what indexes needs to be updated
	oldFltrs := utils.NewStringSet(*oldFilterIds)
	newFltrs := utils.NewStringSet(newFilterIDs)

	oldFilterIDs := make([]string, 0, len(*oldFilterIds))
	newFilterIDs = make([]string, 0, len(newFilterIDs))

	for fltrID := range oldFltrs {
		if !newFltrs.Has(fltrID) { // append only if the index needs to be removed
			oldFilterIDs = append(oldFilterIDs, fltrID)
		}
	}

	for fltrID := range newFltrs {
		if !oldFltrs.Has(fltrID) { // append only if the index needs to be added
			newFilterIDs = append(newFilterIDs, fltrID)
		}
	}

	if len(oldFilterIDs) != 0 || oldFltrs.Size() == 0 {
		// has some indexes to remove or
		// the old profile doesn't have filters but the new one has so remove the *none index
		if err = removeIndexFiltersItem(dm, idxItmType, tnt, itemID, oldFilterIDs); err != nil {
			return
		}
		if err = removeItemFromFilterIndex(dm, idxItmType, tnt, ctx, itemID, oldFilterIDs); err != nil {
			return
		}
	}

	if len(newFilterIDs) != 0 || newFltrs.Size() == 0 {
		// has some indexes to add or
		// the old profile has filters but the new one does not so add the *none index
		if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, newFilterIDs); err != nil {
			return
		}
		if err = addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, newFilterIDs); err != nil {
			return
		}
	}
	return
}

// updatedIndexesWithContexts will compare the old contexts with the new ones and only uptdate what is needed
// this is used by the profiles that have context(e.g. AttributeProfile)
func updatedIndexesWithContexts(dm *DataManager, idxItmType, tnt, itemID string,
	oldContexts, oldFilterIDs *[]string, newContexts, newFilterIDs []string) (err error) {
	if oldContexts == nil { // new profile add all indexes
		if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, newFilterIDs); err != nil {
			return
		}
		for _, ctx := range newContexts {
			if err = addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, newFilterIDs); err != nil {
				return
			}
		}
		return
	}

	oldCtx := utils.NewStringSet(*oldContexts)
	newCtx := utils.NewStringSet(newContexts)

	// split the contexts in three categories
	removeContexts := make([]string, 0, len(*oldContexts))
	addContexts := make([]string, 0, len(newContexts))
	updateContexts := make([]string, 0, len(newContexts))

	for ctx := range oldCtx {
		if !newCtx.Has(ctx) { // append only if the index needs to be removed
			removeContexts = append(removeContexts, ctx)
		} else {
			updateContexts = append(updateContexts, ctx)
		}
	}

	for ctx := range newCtx {
		if !oldCtx.Has(ctx) { // append only if the index needs to be added
			addContexts = append(addContexts, ctx)
		}
	}

	// remove all indexes for the old contexs
	if oldFilterIDs != nil {
		if len(updateContexts) == 0 {
			if err = removeIndexFiltersItem(dm, idxItmType, tnt, itemID, *oldFilterIDs); err != nil {
				return
			}
		}
		for _, ctx := range removeContexts {
			if err = removeItemFromFilterIndex(dm, idxItmType, tnt, ctx, itemID, *oldFilterIDs); err != nil {
				return
			}
		}
	}
	// update the indexes for the contexts tha were not removed
	// in a similar way we do for the profile that do not have contexs
	if len(updateContexts) != 0 {
		if oldFilterIDs == nil { // nothing to remove so just create the new indexes
			if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, newFilterIDs); err != nil {
				return
			}
			for _, ctx := range updateContexts {
				if err = addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, newFilterIDs); err != nil {
					return
				}
			}
		} else if len(*oldFilterIDs) != 0 || len(newFilterIDs) != 0 { // nothing to update
			// check what indexes needs to be updated
			oldFltrs := utils.NewStringSet(*oldFilterIDs)
			newFltrs := utils.NewStringSet(newFilterIDs)

			removeFilterIDs := make([]string, 0, len(*oldFilterIDs))
			addFilterIDs := make([]string, 0, len(newFilterIDs))

			for fltrID := range oldFltrs {
				if !newFltrs.Has(fltrID) { // append only if the index needs to be removed
					removeFilterIDs = append(removeFilterIDs, fltrID)
				}
			}

			for fltrID := range newFltrs {
				if !oldFltrs.Has(fltrID) { // append only if the index needs to be added
					addFilterIDs = append(addFilterIDs, fltrID)
				}
			}

			if len(removeFilterIDs) != 0 || oldFltrs.Size() == 0 {
				// has some indexes to remove or
				// the old profile doesn't have filters but the new one has so remove the *none index
				if err = removeIndexFiltersItem(dm, idxItmType, tnt, itemID, removeFilterIDs); err != nil {
					return
				}
				for _, ctx := range updateContexts {
					if err = removeItemFromFilterIndex(dm, idxItmType, tnt, ctx, itemID, removeFilterIDs); err != nil {
						return
					}
				}
			}

			if len(addFilterIDs) != 0 || newFltrs.Size() == 0 {
				// has some indexes to add or
				// the old profile has filters but the new one does not so add the *none index
				if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, addFilterIDs); err != nil {
					return
				}
				for _, ctx := range updateContexts {
					if err = addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, addFilterIDs); err != nil {
						return
					}
				}
			}
		}
	} else {
		if err = addIndexFiltersItem(dm, idxItmType, tnt, itemID, newFilterIDs); err != nil {
			return
		}
	}

	// add indexes for new contexts
	for _, ctx := range addContexts {
		if err = addItemToFilterIndex(dm, idxItmType, tnt, ctx, itemID, newFilterIDs); err != nil {
			return
		}
	}
	return
}

// splitFilterIndex splits the cache key so it can be used to recache the indexes
func splitFilterIndex(tntCtxIdxKey string) (tntCtx, idxKey string, err error) {
	splt := utils.SplitConcatenatedKey(tntCtxIdxKey) // tntCtx:filterType:fieldName:fieldVal
	lsplt := len(splt)
	if lsplt < 4 {
		err = fmt.Errorf("WRONG_IDX_KEY_FORMAT")
		return
	}
	tntCtx = utils.ConcatenatedKey(splt[:lsplt-3]...) // prefix may contain context/subsystems
	idxKey = utils.ConcatenatedKey(splt[lsplt-3:]...)
	return
}

// ComputeIndexes gets the indexes from tha DB and ensure that the items are indexed
// getFilters returns a list of filters IDs for the given profile id
func ComputeIndexes(dm *DataManager, tnt, ctx, idxItmType string, IDs *[]string,
	transactionID string, getFilters func(tnt, id, ctx string) (*[]string, error)) (processed bool, err error) {
	var profilesIDs []string
	if IDs == nil { // get all items
		var ids []string
		if ids, err = dm.DataDB().GetKeysForPrefix(utils.CacheIndexesToPrefix[idxItmType]); err != nil {
			return
		}
		for _, id := range ids {
			profilesIDs = append(profilesIDs, utils.SplitConcatenatedKey(id)[1])
		}
	} else {
		profilesIDs = *IDs
	}
	tntCtx := tnt
	if ctx != utils.EmptyString {
		tntCtx = utils.ConcatenatedKey(tnt, ctx)
	}
	for _, id := range profilesIDs {
		var filterIDs *[]string
		if filterIDs, err = getFilters(tnt, id, ctx); err != nil {
			return
		}
		if filterIDs == nil {
			continue
		}
		var index map[string]utils.StringSet
		if index, err = newFilterIndex(dm, idxItmType,
			tnt, ctx, id, *filterIDs); err != nil {
			return
		}
		for _, idx := range index {
			idx.Add(id)
		}
		if err = dm.SetIndexes(idxItmType, tntCtx, index, cacheCommit(transactionID), transactionID); err != nil {
			return
		}
		processed = true
	}
	return
}

// addIndexFiltersItem will add a reference for the items in the reverse filter index
func addIndexFiltersItem(dm *DataManager, idxItmType, tnt, itemID string, filterIDs []string) (err error) {
	for _, ID := range filterIDs {
		if strings.HasPrefix(ID, utils.Meta) { // skip inline
			continue
		}
		tntCtx := utils.ConcatenatedKey(tnt, ID)
		var indexes map[string]utils.StringSet
		if indexes, err = dm.GetIndexes(utils.CacheReverseFilterIndexes, tntCtx,
			idxItmType, true, false); err != nil {
			if err != utils.ErrNotFound {
				return
			}
			err = nil
			indexes = map[string]utils.StringSet{
				idxItmType: make(utils.StringSet), // create an empty index if is not found in DB in case we add them later
			}
		}
		indexes[idxItmType].Add(itemID)

		if err = dm.SetIndexes(utils.CacheReverseFilterIndexes, tntCtx, indexes, true, utils.NonTransactional); err != nil {
			return
		}
		for indxKey := range indexes {
			if err = Cache.Remove(utils.CacheReverseFilterIndexes, utils.ConcatenatedKey(tntCtx, indxKey), true, utils.NonTransactional); err != nil {
				return
			}
		}
	}
	return
}

// addIndexFiltersItem will removes a reference for the items in the reverse filter index
func removeIndexFiltersItem(dm *DataManager, idxItmType, tnt, itemID string, filterIDs []string) (err error) {
	for _, ID := range filterIDs {
		if strings.HasPrefix(ID, utils.Meta) { // skip inline
			continue
		}
		tntCtx := utils.ConcatenatedKey(tnt, ID)
		var indexes map[string]utils.StringSet
		if indexes, err = dm.GetIndexes(utils.CacheReverseFilterIndexes, tntCtx,
			idxItmType, true, false); err != nil {
			if err != utils.ErrNotFound {
				return
			}
			err = nil
			continue // it is already removed
		}
		indexes[idxItmType].Remove(itemID)

		if err = dm.SetIndexes(utils.CacheReverseFilterIndexes, tntCtx, indexes, true, utils.NonTransactional); err != nil {
			return
		}
		for indxKey := range indexes {
			if err = Cache.Remove(utils.CacheReverseFilterIndexes, utils.ConcatenatedKey(tntCtx, indxKey), true, utils.NonTransactional); err != nil {
				return
			}
		}
	}
	return
}

// updateFilterIndex  will update the indexes for the new Filter
// we do not care what is added
func updateFilterIndex(dm *DataManager, oldFlt, newFlt *Filter) (err error) {
	if oldFlt == nil { // no filter before so no index to update
		return // nothing to update
	}

	// split the rules so we can determine if we need to update the indexes
	oldRules := utils.StringSet{}
	newRules := utils.StringSet{}    // we only need to determine if we added new rules to rebuild
	removeRules := utils.StringSet{} // but we need to know what indexes to remove
	for _, flt := range newFlt.Rules {
		if !utils.SliceHasMember([]string{utils.MetaPrefix, utils.MetaString}, flt.Type) {
			continue
		}
		for _, fldVal := range flt.Values {
			newRules.Add(utils.ConcatenatedKey(flt.Type, flt.Element, fldVal))
		}
	}
	for _, flt := range oldFlt.Rules {
		if !utils.SliceHasMember([]string{utils.MetaPrefix, utils.MetaString}, flt.Type) {
			continue
		}
		for _, fldVal := range flt.Values {
			if key := utils.ConcatenatedKey(flt.Type, flt.Element, fldVal); !newRules.Has(key) {
				removeRules.Add(key)
			} else {
				oldRules.Add(key)
			}
		}
	}
	needsRebuild := removeRules.Size() != 0 // nothing to remove means nothing to rebuild
	if !needsRebuild {                      // so check if we added somrthing
		for key := range newRules {
			if needsRebuild = !oldRules.Has(key); needsRebuild {
				break
			}
		}
		if !needsRebuild { // if we did not remove or add we do not need to rebuild the indexes
			return
		}
	}

	var rcvIndx map[string]utils.StringSet
	if rcvIndx, err = dm.GetIndexes(utils.CacheReverseFilterIndexes, newFlt.TenantID(),
		utils.EmptyString, true, true); err != nil {
		if err != utils.ErrNotFound {
			return
		}
		err = nil // no index for this filter so  no update needed
		return
	}
	removeIndexKeys := removeRules.AsSlice()

	for idxItmType, indx := range rcvIndx {
		switch idxItmType {
		case utils.CacheThresholdFilterIndexes:
			if err = removeFilterIndexesForFilrer(dm, idxItmType, newFlt.Tenant, // remove the indexes for the filter
				removeIndexKeys, indx); err != nil {
				return
			}
			idxSlice := indx.AsSlice()
			if _, err = ComputeIndexes(dm, newFlt.Tenant, utils.EmptyString, idxItmType, // compute all the indexes for afected items
				&idxSlice, utils.NonTransactional, func(tnt, id, ctx string) (*[]string, error) {
					th, e := dm.GetThresholdProfile(tnt, id, true, false, utils.NonTransactional)
					if e != nil {
						return nil, e
					}
					fltrIDs := make([]string, len(th.FilterIDs))
					for i, fltrID := range th.FilterIDs {
						fltrIDs[i] = fltrID
					}
					return &fltrIDs, nil
				}); err != nil && err != utils.ErrNotFound {
				return utils.APIErrorHandler(err)
			}
		case utils.CacheStatFilterIndexes:
			if err = removeFilterIndexesForFilrer(dm, idxItmType, newFlt.Tenant, // remove the indexes for the filter
				removeIndexKeys, indx); err != nil {
				return
			}
			idxSlice := indx.AsSlice()
			if _, err = ComputeIndexes(dm, newFlt.Tenant, utils.EmptyString, idxItmType, // compute all the indexes for afected items
				&idxSlice, utils.NonTransactional, func(tnt, id, ctx string) (*[]string, error) {
					sq, e := dm.GetStatQueueProfile(tnt, id, true, false, utils.NonTransactional)
					if e != nil {
						return nil, e
					}
					fltrIDs := make([]string, len(sq.FilterIDs))
					for i, fltrID := range sq.FilterIDs {
						fltrIDs[i] = fltrID
					}
					return &fltrIDs, nil
				}); err != nil && err != utils.ErrNotFound {
				return utils.APIErrorHandler(err)
			}
		case utils.CacheResourceFilterIndexes:
			if err = removeFilterIndexesForFilrer(dm, idxItmType, newFlt.Tenant, // remove the indexes for the filter
				removeIndexKeys, indx); err != nil {
				return
			}
			idxSlice := indx.AsSlice()
			if _, err = ComputeIndexes(dm, newFlt.Tenant, utils.EmptyString, idxItmType, // compute all the indexes for afected items
				&idxSlice, utils.NonTransactional, func(tnt, id, ctx string) (*[]string, error) {
					rs, e := dm.GetResourceProfile(tnt, id, true, false, utils.NonTransactional)
					if e != nil {
						return nil, e
					}
					fltrIDs := make([]string, len(rs.FilterIDs))
					for i, fltrID := range rs.FilterIDs {
						fltrIDs[i] = fltrID
					}
					return &fltrIDs, nil
				}); err != nil && err != utils.ErrNotFound {
				return utils.APIErrorHandler(err)
			}
		case utils.CacheRouteFilterIndexes:
			if err = removeFilterIndexesForFilrer(dm, idxItmType, newFlt.Tenant, // remove the indexes for the filter
				removeIndexKeys, indx); err != nil {
				return
			}
			idxSlice := indx.AsSlice()
			if _, err = ComputeIndexes(dm, newFlt.Tenant, utils.EmptyString, idxItmType, // compute all the indexes for afected items
				&idxSlice, utils.NonTransactional, func(tnt, id, ctx string) (*[]string, error) {
					rt, e := dm.GetRouteProfile(tnt, id, true, false, utils.NonTransactional)
					if e != nil {
						return nil, e
					}
					fltrIDs := make([]string, len(rt.FilterIDs))
					for i, fltrID := range rt.FilterIDs {
						fltrIDs[i] = fltrID
					}
					return &fltrIDs, nil
				}); err != nil && err != utils.ErrNotFound {
				return utils.APIErrorHandler(err)
			}
		case utils.CacheChargerFilterIndexes:
			if err = removeFilterIndexesForFilrer(dm, idxItmType, newFlt.Tenant, // remove the indexes for the filter
				removeIndexKeys, indx); err != nil {
				return
			}
			idxSlice := indx.AsSlice()
			if _, err = ComputeIndexes(dm, newFlt.Tenant, utils.EmptyString, idxItmType, // compute all the indexes for afected items
				&idxSlice, utils.NonTransactional, func(tnt, id, ctx string) (*[]string, error) {
					ch, e := dm.GetChargerProfile(tnt, id, true, false, utils.NonTransactional)
					if e != nil {
						return nil, e
					}
					fltrIDs := make([]string, len(ch.FilterIDs))
					for i, fltrID := range ch.FilterIDs {
						fltrIDs[i] = fltrID
					}
					return &fltrIDs, nil
				}); err != nil && err != utils.ErrNotFound {
				return utils.APIErrorHandler(err)
			}
		case utils.CacheAttributeFilterIndexes:
			for itemID := range indx {
				var ap *AttributeProfile
				if ap, err = dm.GetAttributeProfile(newFlt.Tenant, itemID,
					true, false, utils.NonTransactional); err != nil {
					return
				}
				for _, ctx := range ap.Contexts {
					if err = removeFilterIndexesForFilrer(dm, idxItmType,
						utils.ConcatenatedKey(newFlt.Tenant, ctx), // remove the indexes for the filter
						removeIndexKeys, indx); err != nil {
						return
					}
					var updIdx map[string]utils.StringSet
					if updIdx, err = newFilterIndex(dm, idxItmType,
						newFlt.Tenant, ctx, itemID, ap.FilterIDs); err != nil {
						return
					}
					for _, idx := range updIdx {
						idx.Add(itemID)
					}
					if err = dm.SetIndexes(idxItmType, utils.ConcatenatedKey(newFlt.Tenant, ctx),
						updIdx, false, utils.NonTransactional); err != nil {
						return
					}
				}
			}
		case utils.CacheDispatcherFilterIndexes:
			for itemID := range indx {
				var dp *DispatcherProfile
				if dp, err = dm.GetDispatcherProfile(newFlt.Tenant, itemID,
					true, false, utils.NonTransactional); err != nil {
					return
				}
				for _, ctx := range dp.Subsystems {
					if err = removeFilterIndexesForFilrer(dm, idxItmType,
						utils.ConcatenatedKey(newFlt.Tenant, ctx), // remove the indexes for the filter
						removeIndexKeys, indx); err != nil {
						return
					}
					var updIdx map[string]utils.StringSet
					if updIdx, err = newFilterIndex(dm, idxItmType,
						newFlt.Tenant, ctx, itemID, dp.FilterIDs); err != nil {
						return
					}
					for _, idx := range updIdx {
						idx.Add(itemID)
					}
					if err = dm.SetIndexes(idxItmType, utils.ConcatenatedKey(newFlt.Tenant, ctx),
						updIdx, false, utils.NonTransactional); err != nil {
						return
					}
				}
			}
		}
	}
	return
}

// removeFilterIndexesForFilrer removes the itemID for the index keys
func removeFilterIndexesForFilrer(dm *DataManager, idxItmType, tnt string,
	removeIndexKeys []string, itemIDs utils.StringSet) (err error) {
	for _, idxKey := range removeIndexKeys { // delete old filters indexes for this item
		var remIndx map[string]utils.StringSet
		if remIndx, err = dm.GetIndexes(idxItmType, tnt,
			idxKey, true, false); err != nil {
			if err != utils.ErrNotFound {
				return
			}
			err = nil
			continue
		}
		for idx := range itemIDs {
			remIndx[idxKey].Remove(idx)
		}

		// for indxKey := range remIndx {
		// 	if err = Cache.Remove(idxItmType, utils.ConcatenatedKey(tnt, indxKey),
		// 		true, utils.NonTransactional); err != nil {
		// 		return
		// 	}
		// }
		if err = dm.SetIndexes(idxItmType, tnt, remIndx, true, utils.NonTransactional); err != nil {
			return
		}
	}
	return
}
