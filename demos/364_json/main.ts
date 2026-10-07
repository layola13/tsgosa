function main(): i32 {
  console.log(JSON.stringify(42));
  console.log(JSON.stringify("a"));
  console.log(JSON.stringify(true));
  console.log(JSON.stringify(null));
  const a: number[] = [1, 2];
  console.log(JSON.stringify(a));
  const b: string[] = ["x", "yy"];
  console.log(JSON.stringify(b));
  return 0;
}
