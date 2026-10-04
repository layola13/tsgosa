class Box {
  v: i32;
  constructor(v: i32) {
    this.v = v;
  }
  get double(): i32 {
    return this.v + this.v;
  }
  set double(x: i32) {
    this.v = x;
  }
}
function main(): i32 {
  const b = new Box(21);
  console.log(b.double);
  b.double = 10;
  console.log(b.double, b.v);
  return 0;
}
