function main(): i32 {
  let fizz: i32 = 0;
  let buzz: i32 = 0;
  let both: i32 = 0;
  for (let i: i32 = 1; i <= 15; i++) {
    if (i % 15 == 0) {
      both = both + 1;
    } else if (i % 3 == 0) {
      fizz = fizz + 1;
    } else if (i % 5 == 0) {
      buzz = buzz + 1;
    }
  }
  console.log(fizz, buzz, both);
  return 0;
}
