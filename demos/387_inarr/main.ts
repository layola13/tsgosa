function main(): i32 {
  let a: i32[] = [10, 20, 30];
  if (1 in a) { console.log(1); } else { console.log(0); }
  if (3 in a) { console.log(1); } else { console.log(0); }
  let i: i32 = 0;
  if (i in a) { console.log(1); } else { console.log(0); }
  console.log(2 in a);
  if (-1 in a) { console.log(1); } else { console.log(0); }
  return 0;
}
