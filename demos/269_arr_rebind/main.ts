function main(): i32 {
  let a = [1];
  a = a.concat([9]);
  console.log(a.length);
  let i = 0;
  while (i < 2) {
    a = a.concat([7]);
    i++;
  }
  console.log(a.length);
  console.log(a[0] + a[3]);
  return 0;
}
