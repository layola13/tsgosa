class C {
  x: i32;
  constructor() {
    this.x = 3;
  }
  get(): i32 {
    return this.x;
  }
}
function main(): i32 {
  const c = new C();
  console.log(c.get());
  return 0;
}
