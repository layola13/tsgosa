function main(): i32 {
  const re = /ab+c/;
  const m = "xxabbbcyy".match(re);
  console.log(m.length);
  console.log(m[0]);
  return 0;
}
