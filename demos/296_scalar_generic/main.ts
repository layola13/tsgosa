function first<T>(arr: T[]): i32 {
  return arr.length;
}
function firstC<const T>(arr: T[]): i32 {
  return arr.length;
}
function head<T extends i32[]>(arr: T): i32 {
  return arr[0];
}
function assertI32(x: i32): asserts x {
  console.log(x);
}
function noval(): i32 {
  console.log(1);
}
function main(): i32 {
  const b: bigint = 10;
  const v: void = undefined;
  assertI32(1);
  noval();
  const r: i32 = first([1, 2]) + firstC([3]) + head([4, 5]);
  console.log(b + r);
  return b + r;
}
console.log(main());
