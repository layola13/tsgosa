class A {
  v: i32 = 5;
  w: i32 = 7;
  run(): i32 {
    const { v, w } = this;
    return v + w;
  }
}
function main(): number {
  const a = new A();
  return a.run();
}
console.log(main());
