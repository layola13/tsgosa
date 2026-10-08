class C {
  v: i32 = 7;
  getf(): i32 {
    const g = (): i32 => {
      return this.v;
    };
    return g();
  }
}
function main(): i32 {
  const c = new C();
  console.log(c.getf());
  return 0;
}
