function getArr(): number[] {
  return [8, 9];
}
class C {
  n: number = 0;
  constructor(a: number[]|null) { this.n = getArr()?.[0] ?? -1; }
}
function main(): i32 {
  const c = new C([8, 9]);
  console.log(c.n);
  return 0;
}
