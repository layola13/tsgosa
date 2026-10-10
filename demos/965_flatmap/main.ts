function main(): i32 {
  const a: i32[] = [1, 2, 3];
  console.log(a.flatMap((v: i32) => [v, v]).length);
  return 0;
}
