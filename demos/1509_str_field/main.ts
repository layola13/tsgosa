interface Q { name: string; age: i32; }
function main(): i32 {
  const q: Q = { name: "hi", age: 3 };
  console.log(q.name);
  console.log(q.age);
  return 0;
}
