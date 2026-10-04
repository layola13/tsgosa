namespace N {
  export class C {
    x: i32 = 0;
    constructor(x: i32) {
      this.x = x;
    }
  }
}
class D extends N.C {
  y: i32 = 0;
  constructor(x: i32, y: i32) {
    super(x);
    this.y = y;
  }
}
function main(): i32 {
  const d = new D(3, 4);
  return d.x + d.y;
}
console.log(main());
