function main(): i32 {
  const a: i32[] = [10, 20, 30];
  console.log(a.includes(20) ? 1 : 0);
  console.log(a.indexOf(30));
  console.log(a.lastIndexOf(10));
  return 0;
}
