class C {
  x: i32;
  constructor() {
    this.x = 3;
  }
}
function main(): i32 {
  const c = new C();
  console.log(c.x);
  return 0;
}
