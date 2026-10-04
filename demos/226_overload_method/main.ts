class Box {
  v: i32 = 0;
  set(x: i32): void;
  set(x: boolean): void;
  set(x: i32): void {
    this.v = x;
  }
}
function main(): i32 {
  const b = new Box();
  b.set(40);
  console.log(b.v);
  b.set(true);
  console.log(b.v);
  return 0;
}
