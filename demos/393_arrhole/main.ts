function main(): i32 {
  let a: i32[] = [1, , 3];
  console.log(a.length);
  console.log(a[0] + a[1] + a[2]);
  return 0;
}
