class A { protected v: i32 = 5; }
class B extends A { }
class C extends B {
  get(): i32 { return this.v + 1; }
  add(x: i32): i32 { return this.v + x; }
}
function main(): i32 {
  console.log(new C().get());
  const s: i32 = new C().add(10);
  console.log(s);
  return 0;
}
