class Box {
  v: i32 = 5;
  get double(): i32 {
    return this.v * 2;
  }
}
function main(): i32 {
  const b = new Box();
  console.log(b.double);
  return 0;
}
