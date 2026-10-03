class Acc {
  total: i32 = 100;
  add(x: i32): i32 {
    this.total = this.total + x;
    return this.total;
  }
}
function main(): i32 {
  const a = new Acc();
  console.log(a.add(5));
  return 0;
}
