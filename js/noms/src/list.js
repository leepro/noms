// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

// @flow

import type {makeChunkFn} from './sequence-chunker.js';
import type {ValueReader, ValueReadWriter} from './value-store.js';
import type {Splice} from './edit-distance.js';
import type Value, {ValueCallback} from './value.js'; // eslint-disable-line no-unused-vars
import type {AsyncIterator} from './async-iterator.js';
import {default as SequenceChunker, chunkSequence, chunkSequenceSync} from './sequence-chunker.js';
import Collection from './collection.js';
import {IndexedSequenceIterator, newCursorAtIndex} from './indexed-sequence.js';
import {diff} from './indexed-sequence-diff.js';
import {invariant} from './assert.js';
import {
  newIndexedMetaSequenceChunkFn,
} from './meta-sequence.js';
import {makeListType, makeUnionType, getTypeOfValue} from './type.js';
import {equals} from './compare.js';
import {Kind} from './noms-kind.js';
import {DEFAULT_MAX_SPLICE_MATRIX_SIZE} from './edit-distance.js';
import {hashValueBytes} from './rolling-value-hasher.js';
import Sequence, {OrderedKey} from './sequence.js';

function newListLeafChunkFn<T: Value>(vr: ?ValueReader): makeChunkFn<any, any> {
  return (items: Array<T>) => {
    const seq = newListLeafSequence(vr, items);
    const list = List.fromSequence(seq);
    const key = new OrderedKey(items.length);
    return [list, key, items.length];
  };
}

/**
 * List represents a list or an array of Noms values. A list can contain zero or more values of zero
 * or more types. The type of the list will reflect the type of the elements in the list. For
 * example:
 *
 *  const l = new List([1, true]);
 *  console.log(l.type.describe());
 *  // outputs List<Bool | Number>
 *
 * Lists, like all Noms values are immutable so the "mutation" methods return a new list.
 */
export default class List<T: Value> extends Collection<Sequence<any>> {
  constructor(values: Array<T> = []) {
    const seq = chunkSequenceSync(
        values,
        newListLeafChunkFn(null),
        newIndexedMetaSequenceChunkFn(Kind.List, null, null),
        hashValueBytes);
    super(seq);
  }

  walkValues(vr: ValueReader, cb: ValueCallback): Promise<void> {
    return this.forEach(v => cb(v));
  }

  /**
   * Get returns the value at index `idx`. If this list has been chunked then this will have to
   * descend into the prolly-tree which leads to Get being O(depth).
   */
  async get(idx: number): Promise<T> {
    invariant(idx >= 0 && idx < this.length);
    return newCursorAtIndex(this.sequence, idx).then(cursor => cursor.getCurrent());
  }

  /**
   * Splice returns a new list where `deleteCount` values have been removed at `idx` and the values
   * `insert` have been inserted instead.
   */
  splice(idx: number, deleteCount: number, ...insert: Array<T>): Promise<List<T>> {
    const vr = this.sequence.vr;
    return newCursorAtIndex(this.sequence, idx).then(cursor =>
      chunkSequence(cursor, vr, insert, deleteCount, newListLeafChunkFn(vr),
                    newIndexedMetaSequenceChunkFn(Kind.List, vr, null),
                    hashValueBytes)).then(s => List.fromSequence(s));
  }

  /**
   * Insert returns a new list where `values` have been inserted at `idx`.
   */
  insert(idx: number, ...values: Array<T>): Promise<List<T>> {
    return this.splice(idx, 0, ...values);
  }

  /**
   * Returns a new list where a single element at index `idx` have been removed.
   */
  remove(idx: number): Promise<List<T>> {
    return this.splice(idx, 1);
  }

  /**
   * This returns a new list where `values` have been appended to the resulting list.
   */
  append(...values: Array<T>): Promise<List<T>> {
    return this.splice(this.length, 0, ...values);
  }

  /**
   * This calls a function for every value in the list. This function is async and returns a promise
   * that will be fulfilled when all the callback functions have been called. If the callback
   * function returns a promise `forEach` will continue but it will not return until all of those
   * promises have been fulfilled.
   */
  async forEach(cb: (v: T, i: number) => ?Promise<any>): Promise<void> {
    const cursor = await newCursorAtIndex(this.sequence, 0, true);
    const promises = [];
    await cursor.iter((v, i) => {
      promises.push(cb(v, i));
      return false;
    });
    await Promise.all(promises);
  }

  /**
   * Returns a new `AsyncIterator` which can be used to iterate over the list.
   */
  iterator(): AsyncIterator<T> {
    return new IndexedSequenceIterator(newCursorAtIndex(this.sequence, 0));
  }

  /**
   * Returns a new `AsyncIterator` starting at `i` which can be used to iterate over the list.
   */
  iteratorAt(i: number): AsyncIterator<T> {
    return new IndexedSequenceIterator(newCursorAtIndex(this.sequence, i));
  }

  /**
   * Diff returns the diff of two different lists. If `maxSpliceMatrixSize` is provided then that
   * determines the how big of an edit distance matrix we are willing to compute versus just saying
   * the thing changed.
   */
  diff(last: List<T>,
       maxSpliceMatrixSize: number = DEFAULT_MAX_SPLICE_MATRIX_SIZE): Promise<Array<Splice>> {
    invariant(equals(this.type, last.type));

    if (equals(this, last)) {
      return Promise.resolve([]); // nothing changed.
    }
    if (this.length === 0) {
      return Promise.resolve([[0, last.length, 0, 0]]); // Everything removed
    }
    if (last.length === 0) {
      return Promise.resolve([[0, 0, this.length, 0]]); // Everything added
    }

    return Promise.all([
      newCursorAtIndex(last.sequence, 0),
      newCursorAtIndex(this.sequence, 0),
    ]).then(cursors =>
        diff(last.sequence, cursors[0].depth, 0, this.sequence, cursors[1].depth, 0,
             maxSpliceMatrixSize));
  }

  /**
   * The number of elements in the list.
   */
  get length(): number {
    return this.sequence.numLeaves;
  }
}

/**
 * ListLeafSequence is used for the leaf lists of a list prolly-tree.
 */
export class ListLeafSequence<T: Value> extends Sequence<T> {}

export function newListLeafSequence<T: Value>(vr: ?ValueReader, items: T[]): ListLeafSequence<T> {
  const t = makeListType(makeUnionType(items.map(getTypeOfValue)));
  return new ListLeafSequence(vr, t, items);
}

type ListWriterState = 'writable' | 'closed';

/**
 * ListWriter is a class for efficiently creating a large list.
 */
export class ListWriter<T: Value> {
  _state: ListWriterState;
  _list: ?Promise<List<T>>;
  _chunker: SequenceChunker<T, ListLeafSequence<T>>;
  _vrw: ?ValueReadWriter;

  /**
   * If `vrw` is non `null` the chunks of the list gets written to it as they are created.
   */
  constructor(vrw: ?ValueReadWriter) {
    this._state = 'writable';
    this._chunker = new SequenceChunker(null, vrw, vrw, newListLeafChunkFn(vrw),
        newIndexedMetaSequenceChunkFn(Kind.List, vrw, vrw), hashValueBytes);
    this._vrw = vrw;
  }

  /**
   * Adds an item to the list we are creating.
   */
  write(item: T) {
    assert(this._state === 'writable');
    this._chunker.append(item);
  }

  /**
   * Closes the ListWriter. No more items can be added after this.
   */
  close() {
    assert(this._state === 'writable');
    this._list = this._chunker.done(this._vrw).then(seq => List.fromSequence(seq));
    this._state = 'closed';
  }

  /**
   * Gets a promise to the list we are creating. Make sure you call `close` before trying to get
   * the list.
   */
  get list(): Promise<List<any>> {
    assert(this._state === 'closed');
    invariant(this._list);
    return this._list;
  }
}

function assert(v: any) {
  if (!v) {
    throw new TypeError('Invalid usage of ListWriter');
  }
}
