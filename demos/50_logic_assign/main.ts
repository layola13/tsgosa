function main(): i32 {
  let x: i32 = 0;
  x ||= 7;
  console.log(x);
  let y: i32 = 5;
  y &&= 3;
  console.log(y);
  return 0;
}
