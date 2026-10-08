class B {
  x: i32 = 3;
  get g(): i32 {
    return this.x * 2;
  }
  get h(): i32 {
    return 7;
  }
}
class C extends B {
  get(): i32 {
    return super.g + 1;
  }
  get2(): i32 {
    return super.h + 1;
  }
}
function main(): i32 {
  const c = new C();
  console.log(c.get());
  console.log(c.get2());
  return 0;
}
