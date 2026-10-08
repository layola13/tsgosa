class C {
  v!: i32;
  init(): void {
    this.v = 4;
  }
  get(): i32 {
    return this.v;
  }
}
function main(): i32 {
  const c = new C();
  c.init();
  console.log(c.get());
  return 0;
}
