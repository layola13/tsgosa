export namespace V {
  export class Checker {
    lo: i32 = 0;
    hi: i32 = 0;
    constructor(lo: i32, hi: i32) {
      this.lo = lo;
      this.hi = hi;
    }
    check(x: i32): i32;
    check(x: boolean): i32;
    check(x: i32): i32 {
      if (x < this.lo) {
        return 0;
      }
      if (x > this.hi) {
        return 0;
      }
      return 1;
    }
  }
  export class Strict extends Checker {
    constructor(lo: i32, hi: i32) {
      super(lo, hi);
    }
  }
  export function clamp(x: i32, lo: i32, hi: i32): i32 {
    if (x < lo) {
      return lo;
    }
    if (x > hi) {
      return hi;
    }
    return x;
  }
}
export function inRange(x: i32, lo: i32, hi: i32): i32 {
  if (x < lo) {
    return 0;
  }
  if (x > hi) {
    return 0;
  }
  return 1;
}
export default { inRange };
