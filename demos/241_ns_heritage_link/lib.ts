namespace N {
  export class B {
    x: i32 = 0;
    constructor(x: i32) {
      this.x = x;
    }
  }
  export class D extends B {
    y: i32 = 0;
    constructor(x: i32, y: i32) {
      super(x);
      this.y = y;
    }
  }
}
