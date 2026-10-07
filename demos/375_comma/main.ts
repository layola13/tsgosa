function main(): i32 {
  let x: i32 = 0;
  x = (x + 1, x + 2);
  console.log(x);
  console.log((10, 20));
  return 0;
}
