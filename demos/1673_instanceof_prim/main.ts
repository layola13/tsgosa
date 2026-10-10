function main(): i32 {
  const a: i32[] = [1];
  console.log(a instanceof Array ? 1 : 0);
  const x: i32 = 5;
  console.log(x instanceof Object ? 1 : 0);
  const s: string = "hi";
  console.log(s instanceof Object ? 1 : 0);
  console.log(a instanceof Object ? 1 : 0);
  return 0;
}
