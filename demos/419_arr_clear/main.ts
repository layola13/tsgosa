function main(): i32 {
  let a = [1, 2, 3];
  a.length = 0;
  console.log(a.length);
  a.push(9);
  console.log(a[0]);
  console.log(a.length);
  return 0;
}
